package trainingcsv

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/digocelos/transaction-risk-go/pixinput"
	"github.com/digocelos/transaction-risk-go/trainingdata"
)

// Codes of the problems that belong to the CSV layout. Problems of the
// values use the codes of pixinput and trainingdata.
const (
	CodeInvalidCSV       = "invalid_csv"
	CodeUnknownColumn    = "unknown_column"
	CodeDuplicateColumn  = "duplicate_column"
	CodeMissingColumn    = "missing_column"
	CodeValueWithoutAsOf = "value_without_as_of"
	CodeMemberNotAllowed = "member_not_allowed"
)

// MaxProblems is the number of problems Convert reports before it stops.
const MaxProblems = 1000

// Options configure a conversion. Separator is ',' or ';'. MaxRecords
// limits the rows converted, as trainingdata.Scan does.
type Options struct {
	Separator  rune
	MaxRecords int
}

// Problem is one reason why a row cannot be converted. Row is the line of
// the CSV file where the row starts, 1 for the header; Column is empty when
// the problem concerns the whole row.
type Problem struct {
	Row     int    `json:"row"`
	Column  string `json:"column"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// InvalidError lists the problems of a CSV file that cannot be converted.
// Truncated is set when Convert stopped at MaxProblems.
type InvalidError struct {
	Problems  []Problem
	Truncated bool
}

func (e *InvalidError) Error() string {
	return fmt.Sprintf("the CSV file has %d problems and cannot be converted", len(e.Problems))
}

// Summary describes a converted dataset: the rows converted, the empty rows
// skipped, the records by category as train will classify them, and the
// SHA-256 and size of the JSON Lines written.
type Summary struct {
	Rows          int                      `json:"rows"`
	EmptyRows     int                      `json:"empty_rows"`
	Records       trainingdata.Counts      `json:"records"`
	UsableLabels  trainingdata.LabelCounts `json:"usable_labels"`
	DatasetSHA256 string                   `json:"dataset_sha256"`
	DatasetBytes  int64                    `json:"dataset_bytes"`
}

// Convert reads a CSV file in the pix-training-csv-v1 layout and writes one
// pix-training-example-v2 record per row to w, in row order. Rows whose
// cells are all empty are skipped. Every record is validated with the
// strict pix-training-example-v2 decoder; when any row has a problem,
// Convert returns an *InvalidError with every problem, up to MaxProblems,
// and what it wrote to w must be discarded. More rows than MaxRecords
// return a *trainingdata.RecordLimitError. Other errors are failures to
// read or write.
func Convert(r io.Reader, w io.Writer, options Options) (Summary, error) {
	if options.Separator != ',' && options.Separator != ';' {
		return Summary{}, fmt.Errorf("the separator must be ',' or ';', got %q", options.Separator)
	}
	if options.MaxRecords < 1 {
		return Summary{}, fmt.Errorf("the record limit must be at least 1, got %d", options.MaxRecords)
	}

	// Spreadsheet tools start UTF-8 exports with a byte order mark, which
	// would otherwise stick to the first column name.
	buffered := bufio.NewReader(r)
	if bom, err := buffered.Peek(3); err == nil && bytes.Equal(bom, []byte("\xef\xbb\xbf")) {
		buffered.Discard(3)
	}

	reader := csv.NewReader(buffered)
	reader.Comma = options.Separator
	reader.ReuseRecord = true

	c := &converter{invalid: &InvalidError{}}
	header, err := reader.Read()
	switch {
	case errors.Is(err, io.EOF):
		c.add(Problem{Row: 1, Code: CodeMissingColumn, Message: "the file has no header"})
		return Summary{}, c.invalid
	case err != nil:
		return Summary{}, c.csvError(err)
	}

	if !c.readHeader(header) {
		return Summary{}, c.invalid
	}

	hash := sha256.New()
	counter := &byteCounter{}
	out := io.MultiWriter(w, hash, counter)
	summary := Summary{}

	for {
		row, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if parseErr := (*csv.ParseError)(nil); errors.As(err, &parseErr) && errors.Is(err, csv.ErrFieldCount) {
				c.add(Problem{Row: parseErr.StartLine, Code: CodeInvalidCSV, Message: fmt.Sprintf(
					"the row has %d cells, the header %d", len(row), len(c.columns))})
				if c.full() {
					break
				}
				continue
			}
			return Summary{}, c.csvError(err)
		}

		line, _ := reader.FieldPos(0)
		if allEmpty(row) {
			summary.EmptyRows++
			continue
		}

		summary.Rows++
		if summary.Rows > options.MaxRecords {
			return Summary{}, &trainingdata.RecordLimitError{Limit: options.MaxRecords}
		}

		data := c.build(line, row)
		if data != nil {
			c.check(line, data, &summary)
			if _, err := out.Write(append(data, '\n')); err != nil {
				return Summary{}, fmt.Errorf("write records: %w", err)
			}
		}

		if c.full() {
			break
		}
	}

	if len(c.invalid.Problems) > 0 {
		return Summary{}, c.invalid
	}

	summary.DatasetSHA256 = hex.EncodeToString(hash.Sum(nil))
	summary.DatasetBytes = counter.bytes
	return summary, nil
}

type converter struct {
	invalid *InvalidError
	columns map[string]int
	cells   []string
	row     int
	failed  bool
}

func (c *converter) add(p Problem) {
	if len(c.invalid.Problems) >= MaxProblems {
		c.invalid.Truncated = true
		return
	}
	c.invalid.Problems = append(c.invalid.Problems, p)
}

func (c *converter) full() bool {
	return len(c.invalid.Problems) >= MaxProblems
}

func (c *converter) csvError(err error) error {
	var parseErr *csv.ParseError
	if errors.As(err, &parseErr) {
		c.add(Problem{Row: parseErr.Line, Code: CodeInvalidCSV, Message: parseErr.Err.Error()})
		return c.invalid
	}
	return fmt.Errorf("read CSV: %w", err)
}

// readHeader maps every column of the layout to its index.
func (c *converter) readHeader(header []string) bool {
	c.columns = map[string]int{}
	known := map[string]bool{}
	for _, column := range Columns() {
		known[column.Name] = true
	}

	for i, name := range header {
		switch _, seen := c.columns[name]; {
		case !known[name]:
			c.add(Problem{Row: 1, Column: name, Code: CodeUnknownColumn, Message: fmt.Sprintf("unknown column %q", name)})
		case seen:
			c.add(Problem{Row: 1, Column: name, Code: CodeDuplicateColumn, Message: fmt.Sprintf("column %q appears more than once", name)})
		default:
			c.columns[name] = i
		}
	}

	for _, column := range Columns() {
		if _, ok := c.columns[column.Name]; !ok {
			c.add(Problem{Row: 1, Column: column.Name, Code: CodeMissingColumn, Message: fmt.Sprintf("column %q is missing", column.Name)})
		}
	}

	return len(c.invalid.Problems) == 0
}

func allEmpty(row []string) bool {
	for _, cell := range row {
		if cell != "" {
			return false
		}
	}
	return true
}

func (c *converter) cell(path []string) string {
	return c.cells[c.columns[columnName(path)]]
}

// build writes the JSON of one row, or returns nil when the row breaks the
// layout.
func (c *converter) build(line int, row []string) []byte {
	c.cells, c.row, c.failed = row, line, false

	var buffer bytes.Buffer
	c.write(&buffer, record, nil)
	if c.failed {
		return nil
	}
	return buffer.Bytes()
}

// jsonNumber matches the JSON number syntax. Other integer cells are
// written as strings, so the decoder reports them as the wrong type.
var jsonNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

func (c *converter) write(buffer *bytes.Buffer, n node, path []string) {
	switch n.kind {
	case integer, text:
		value := c.cell(path)
		switch {
		case value == "":
			buffer.WriteString("null")
		case n.kind == integer && jsonNumber.MatchString(value):
			buffer.WriteString(value)
		default:
			writeString(buffer, value)
		}
		return
	case window:
		if c.emptyBelow(n, path) {
			buffer.WriteString("null")
			return
		}
	case object:
		if n.observation && c.cell(append(slices.Clip(path), "as_of")) == "" {
			c.mustBeEmpty(n, path, CodeValueWithoutAsOf, "has a value, but its observation has no as_of")
			buffer.WriteString("null")
			return
		}
	}

	buffer.WriteByte('{')
	first := true
	member := func(name string) {
		if !first {
			buffer.WriteByte(',')
		}
		first = false
		writeString(buffer, name)
		buffer.WriteByte(':')
	}

	switch {
	case path == nil:
		member("training_example_version")
		writeString(buffer, trainingdata.TrainingExampleV2)
	case len(path) == 1 && path[0] == "scoring_input":
		member("contract_version")
		writeString(buffer, pixinput.ContractV2)
	}

	for _, child := range n.children {
		childPath := append(slices.Clip(path), child.name)
		if child.discriminator != "" && c.cells[c.columns[child.discriminator]] == child.omitWhen {
			c.mustBeEmpty(child, childPath, CodeMemberNotAllowed,
				fmt.Sprintf("must be empty when %s is %s", child.discriminator, child.omitWhen))
			continue
		}
		member(child.name)
		c.write(buffer, child, childPath)
	}
	buffer.WriteByte('}')
}

func writeString(buffer *bytes.Buffer, value string) {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.Encode(value)
	buffer.Write(bytes.TrimSuffix(encoded.Bytes(), []byte("\n")))
}

// emptyBelow reports whether every column under n is empty.
func (c *converter) emptyBelow(n node, path []string) bool {
	if n.kind == integer || n.kind == text {
		return c.cell(path) == ""
	}
	for _, child := range n.children {
		if !c.emptyBelow(child, append(slices.Clip(path), child.name)) {
			return false
		}
	}
	return true
}

// mustBeEmpty reports every column under n that has a value.
func (c *converter) mustBeEmpty(n node, path []string, code, message string) {
	if n.kind == integer || n.kind == text {
		if c.cell(path) != "" {
			c.failed = true
			c.add(Problem{Row: c.row, Column: columnName(path), Code: code, Message: columnName(path) + " " + message})
		}
		return
	}
	for _, child := range n.children {
		c.mustBeEmpty(child, append(slices.Clip(path), child.name), code, message)
	}
}

// check validates one record with the pix-training-example-v2 decoder and
// counts it by category.
func (c *converter) check(line int, data []byte, summary *Summary) {
	_, err := trainingdata.Scan(bytes.NewReader(data), trainingdata.PixRiskV1, 1, func(r trainingdata.Record) error {
		summary.Records.Total++
		switch r.Category {
		case trainingdata.Usable:
			summary.Records.Usable++
			if r.Example.Outcome.Label == trainingdata.Fraud {
				summary.UsableLabels.Fraud++
			} else {
				summary.UsableLabels.Legitimate++
			}
		case trainingdata.Insufficient:
			summary.Records.Insufficient++
		case trainingdata.Immature:
			summary.Records.Immature++
		case trainingdata.Invalid:
			summary.Records.Invalid++
			for _, d := range r.Diagnostics {
				c.add(Problem{Row: line, Column: columnOf(d.Path), Code: d.Code, Message: d.Message})
			}
		}
		return nil
	})
	if err != nil {
		c.add(Problem{Row: line, Code: CodeInvalidCSV, Message: err.Error()})
	}
}

// columnOf turns a JSON Pointer into the record into a column name, or the
// prefix of the columns of an object.
func columnOf(pointer string) string {
	segments := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	if pointer == "" {
		return ""
	}
	return columnName(segments)
}

type byteCounter struct{ bytes int64 }

func (b *byteCounter) Write(p []byte) (int, error) {
	b.bytes += int64(len(p))
	return len(p), nil
}
