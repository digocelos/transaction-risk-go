package trainingdata

import (
	"bytes"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/digocelos/transaction-risk-go/internal/strictjson"
	"github.com/digocelos/transaction-risk-go/pixinput"
)

const scoringInputPath = "/scoring_input"

// decodeRecord validates and classifies one line. It returns an error only
// when the feature schema fails for a reason unrelated to the record.
func decodeRecord(line int, data []byte, tooLarge bool, schema FeatureSchema) (Record, error) {
	record := Record{Line: line}

	root, issues := checkLine(line, data, tooLarge)
	if len(issues) == 0 {
		if issue := checkVersion(root); issue != nil {
			issues = []strictjson.Issue{*issue}
		}
	}

	if len(issues) > 0 {
		return invalidRecord(record, issues), nil
	}

	d := &decoder{}
	example, ok := d.example(data, root)

	if !ok || len(d.Issues()) > 0 {
		return invalidRecord(record, d.Issues()), nil
	}

	record.Example = example

	blockers, err := schema.Blockers(example.ScoringInput)
	if err != nil {
		return Record{}, fmt.Errorf("line %d: %s: %w", line, schema.Name, err)
	}

	switch {
	case len(blockers) > 0:
		record.Category = Insufficient
		for _, blocker := range blockers {
			record.Blockers = append(record.Blockers, Blocker{
				Path:   scoringInputPath + blocker.Path,
				Reason: blocker.Reason,
			})
		}
	case example.Outcome.LabelAsOf.Sub(example.ScoringInput.DecisionTime) < LabelMaturity:
		record.Category = Immature
	default:
		record.Category = Usable
	}

	return record, nil
}

func invalidRecord(record Record, issues []strictjson.Issue) Record {
	record.Category = Invalid
	for _, issue := range strictjson.SortIssues(issues) {
		record.Diagnostics = append(record.Diagnostics, Diagnostic{
			Line:    record.Line,
			Path:    issue.Path,
			Code:    issue.Code,
			Message: issue.Message,
		})
	}
	return record
}

// checkLine applies the line rules. Its first failure is the only issue,
// except duplicate members, which are all reported.
func checkLine(line int, data []byte, tooLarge bool) (*strictjson.Value, []strictjson.Issue) {
	single := func(code, message string) []strictjson.Issue {
		return []strictjson.Issue{{Path: "", Code: code, Message: message}}
	}

	switch {
	case tooLarge:
		return nil, single(CodeRecordTooLarge, fmt.Sprintf("record exceeds %d bytes", MaxRecordSize))
	case line == 1 && bytes.HasPrefix(data, []byte("\xEF\xBB\xBF")):
		return nil, single(strictjson.CodeBOMNotAllowed, "dataset must not start with a byte order mark")
	case bytes.IndexByte(data, '\r') >= 0:
		return nil, single(CodeCarriageReturnNotAllowed, "record must not contain a carriage return")
	case !utf8.Valid(data):
		return nil, single(strictjson.CodeInvalidUTF8, "record is not valid UTF-8")
	case len(bytes.Trim(data, " \t")) == 0:
		return nil, single(CodeBlankLine, "line is blank")
	}

	root, duplicates, err := strictjson.Parse(data)

	switch {
	case errors.Is(err, strictjson.ErrTrailingData):
		return nil, single(strictjson.CodeTrailingData, "only spaces and tabs may follow the record")
	case err != nil:
		return nil, single(strictjson.CodeInvalidJSON, "record is not valid JSON")
	case root.Kind != strictjson.KindObject:
		return nil, single(strictjson.CodeWrongType, "record must be a JSON object")
	}

	var issues []strictjson.Issue
	for _, path := range duplicates {
		issues = append(issues, strictjson.Issue{
			Path:    path,
			Code:    strictjson.CodeDuplicateMember,
			Message: "member appears more than once in the same object",
		})
	}

	return root, issues
}

// checkVersion locates the training example version without assuming any
// version's structure.
func checkVersion(root *strictjson.Value) *strictjson.Issue {
	const path = "/training_example_version"

	value, ok := root.Members["training_example_version"]

	switch {
	case !ok:
		return &strictjson.Issue{Path: path, Code: strictjson.CodeRequired,
			Message: "training_example_version is required"}
	case value.Kind == strictjson.KindNull:
		return &strictjson.Issue{Path: path, Code: strictjson.CodeNullNotAllowed,
			Message: "training_example_version must not be null"}
	case value.Kind != strictjson.KindString:
		return &strictjson.Issue{Path: path, Code: strictjson.CodeWrongType,
			Message: "training_example_version must be a string"}
	case value.Text != TrainingExampleVersion:
		return &strictjson.Issue{Path: path, Code: CodeUnsupportedTrainingExampleVersion,
			Message: fmt.Sprintf("unsupported training example version %q", value.Text)}
	}

	return nil
}

// decoder applies the record structure and consistency rules.
type decoder struct {
	strictjson.Validator
}

// example decodes a record whose line and version checks passed. It
// reports false when the record is invalid.
func (d *decoder) example(data []byte, root *strictjson.Value) (Example, bool) {
	d.Object("", root, "training_example_version", "scoring_input", "outcome", "group_key")

	input, inputOK := d.scoringInput(data, root)
	outcome, labelAsOfOK, lossState := d.outcome(root)

	groupKey, keyState := d.String("", root, "group_key", true, isGroupKey, "^[A-Za-z0-9_-]{1,64}$")

	if inputOK && labelAsOfOK && outcome.LabelAsOf.Before(input.DecisionTime) {
		d.Add("/outcome/label_as_of", CodeLabelBeforeDecisionTime,
			"label_as_of must not be earlier than the scoring input's decision_time")
	}

	switch {
	case outcome.Label == Legitimate && lossState != strictjson.Invalid &&
		(outcome.LossAmountCents == nil || *outcome.LossAmountCents != 0):
		d.Add("/outcome/loss_amount_cents", CodeLossAmountMismatch,
			"a legitimate example must have a loss of 0")
	case outcome.Label == Fraud && lossState == strictjson.Valid && inputOK &&
		*outcome.LossAmountCents > input.PixPayment.AmountCents:
		d.Add("/outcome/loss_amount_cents", CodeLossExceedsAmount,
			"loss_amount_cents must not exceed the payment's amount_cents")
	}

	example := Example{ScoringInput: input, Outcome: outcome}
	if keyState == strictjson.Valid {
		example.GroupKey = &groupKey
	}

	return example, inputOK
}

// scoringInput validates the embedded scoring input from its exact bytes
// with pixinput, reporting its issues under /scoring_input.
func (d *decoder) scoringInput(data []byte, root *strictjson.Value) (pixinput.ScoringInput, bool) {
	value, path, state := d.Member("", root, "scoring_input", false)
	if state != strictjson.Valid {
		return pixinput.ScoringInput{}, false
	}

	if value.Kind != strictjson.KindObject {
		d.Add(path, strictjson.CodeWrongType, "scoring_input must be an object")
		return pixinput.ScoringInput{}, false
	}

	input, err := pixinput.Decode(data[value.Start:value.End])

	var invalid *pixinput.ValidationError
	if errors.As(err, &invalid) {
		for _, issue := range invalid.Issues {
			d.Add(scoringInputPath+issue.Path, issue.Code, "%s", issue.Message)
		}
		return pixinput.ScoringInput{}, false
	}

	return input, err == nil
}

// outcome reads the outcome object. It also reports whether label_as_of is
// valid and the state of loss_amount_cents.
func (d *decoder) outcome(root *strictjson.Value) (Outcome, bool, strictjson.State) {
	object, path := d.ObjectMember("", root, "outcome", false, "label", "label_as_of", "loss_amount_cents")
	if object == nil {
		return Outcome{}, false, strictjson.Invalid
	}

	label := Label(d.Enum(path, object, "label", string(Fraud), string(Legitimate)))
	labelAsOf, labelAsOfOK := d.Timestamp(path, object, "label_as_of")
	loss, lossState := d.Integer(path, object, "loss_amount_cents", 0, true)

	outcome := Outcome{Label: label, LabelAsOf: labelAsOf}
	if lossState == strictjson.Valid {
		outcome.LossAmountCents = &loss
	}

	return outcome, labelAsOfOK, lossState
}

// isGroupKey matches ^[A-Za-z0-9_-]{1,64}$.
func isGroupKey(text string) bool {
	if len(text) < 1 || len(text) > 64 {
		return false
	}

	for i := 0; i < len(text); i++ {
		c := text[i]
		if !(c >= 'A' && c <= 'Z') && !(c >= 'a' && c <= 'z') &&
			!(c >= '0' && c <= '9') && c != '_' && c != '-' {
			return false
		}
	}

	return true
}
