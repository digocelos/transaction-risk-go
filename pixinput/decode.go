package pixinput

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"time"
	"unicode/utf8"

	"github.com/digocelos/transaction-risk-go/internal/strictjson"
)

// Read reads one scoring input document and decodes it. It stops reading
// as soon as the document exceeds MaxDocumentSize. A reader failure is
// returned as is; a document that breaks the contract yields a
// *ValidationError.
func Read(r io.Reader) (ScoringInput, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxDocumentSize+1))
	if err != nil {
		return ScoringInput{}, fmt.Errorf("read scoring input: %w", err)
	}

	return Decode(data)
}

// Decode validates a scoring input document and returns its typed form.
// A document that breaks the contract yields a *ValidationError.
func Decode(data []byte) (ScoringInput, error) {
	root, issues := parseDocument(data)
	if len(issues) > 0 {
		return ScoringInput{}, newValidationError(issues)
	}

	if issue := checkContractVersion(root); issue != nil {
		return ScoringInput{}, newValidationError([]strictjson.Issue{*issue})
	}

	d := &decoder{}
	input := d.scoringInput(root)

	if len(d.Issues()) > 0 {
		return ScoringInput{}, newValidationError(d.Issues())
	}

	return input, nil
}

// parseDocument applies the document rules. When it returns issues,
// validation stops: a document with duplicate members is ambiguous.
func parseDocument(data []byte) (*strictjson.Value, []strictjson.Issue) {
	switch {
	case len(data) > MaxDocumentSize:
		return nil, []strictjson.Issue{{
			Path:    "",
			Code:    CodeDocumentTooLarge,
			Message: fmt.Sprintf("document exceeds %d bytes", MaxDocumentSize),
		}}
	case bytes.HasPrefix(data, []byte("\xEF\xBB\xBF")):
		return nil, []strictjson.Issue{{
			Path:    "",
			Code:    CodeBOMNotAllowed,
			Message: "document must not start with a byte order mark",
		}}
	case !utf8.Valid(data):
		return nil, []strictjson.Issue{{Path: "", Code: CodeInvalidUTF8, Message: "document is not valid UTF-8"}}
	}

	root, duplicates, err := strictjson.Parse(data)

	switch {
	case errors.Is(err, strictjson.ErrTrailingData):
		return nil, []strictjson.Issue{{
			Path:    "",
			Code:    CodeTrailingData,
			Message: "only JSON whitespace may follow the top-level value",
		}}
	case err != nil:
		return nil, []strictjson.Issue{{Path: "", Code: CodeInvalidJSON, Message: "document is not valid JSON"}}
	case root.Kind != strictjson.KindObject:
		return nil, []strictjson.Issue{{Path: "", Code: CodeWrongType, Message: "document must be a JSON object"}}
	}

	var issues []strictjson.Issue
	for _, path := range duplicates {
		issues = append(issues, strictjson.Issue{
			Path:    path,
			Code:    CodeDuplicateMember,
			Message: "member appears more than once in the same object",
		})
	}

	return root, issues
}

// checkContractVersion locates the contract version without assuming any
// version's structure.
func checkContractVersion(root *strictjson.Value) *strictjson.Issue {
	const path = "/contract_version"

	value, ok := root.Members["contract_version"]

	switch {
	case !ok:
		return &strictjson.Issue{Path: path, Code: CodeRequired, Message: "contract_version is required"}
	case value.Kind == strictjson.KindNull:
		return &strictjson.Issue{Path: path, Code: CodeNullNotAllowed, Message: "contract_version must not be null"}
	case value.Kind != strictjson.KindString:
		return &strictjson.Issue{Path: path, Code: CodeWrongType, Message: "contract_version must be a string"}
	case value.Text != ContractVersion:
		return &strictjson.Issue{
			Path:    path,
			Code:    CodeUnsupportedContractVersion,
			Message: fmt.Sprintf("unsupported contract version %q", value.Text),
		}
	}

	return nil
}

// decoder walks a parsed document and collects issues.
type decoder struct {
	strictjson.Validator
	asOfs []timedMember
}

// timedMember is a valid as_of value and its path.
type timedMember struct {
	path string
	time time.Time
}

// keyType reads the open string key_type, preserving it exactly.
func (d *decoder) keyType(path string, parent *strictjson.Value) string {
	keyType, _ := d.String(path, parent, "key_type", false, isKeyType, "^[A-Z][A-Z0-9_]{0,31}$")
	return keyType
}

// asOf reads an observation's as_of and records it for the check against
// decision_time.
func (d *decoder) asOf(path string, parent *strictjson.Value) (time.Time, bool) {
	asOf, ok := d.Timestamp(path, parent, "as_of")
	if ok {
		d.asOfs = append(d.asOfs, timedMember{strictjson.Pointer(path, "as_of"), asOf})
	}
	return asOf, ok
}

// asOfValue reads an observation's as_of when no other rule needs it.
func (d *decoder) asOfValue(path string, parent *strictjson.Value) time.Time {
	asOf, _ := d.asOf(path, parent)
	return asOf
}

// notAfterAsOf reports a date of an observation that is later than its as_of.
func (d *decoder) notAfterAsOf(path, name string, date, asOf time.Time) {
	if date.After(asOf) {
		d.Add(strictjson.Pointer(path, name), CodeDateAfterAsOf, "%s must not be later than as_of", name)
	}
}

func (d *decoder) scoringInput(root *strictjson.Value) ScoringInput {
	d.Object("", root, "contract_version", "decision_time", "pix_payment", "observations")

	decisionTime, decisionOK := d.Timestamp("", root, "decision_time")
	payment := d.pixPayment(root)
	observations := d.observations(root, payment)

	if decisionOK {
		for _, asOf := range d.asOfs {
			if asOf.time.After(decisionTime) {
				d.Add(asOf.path, CodeAsOfAfterDecisionTime,
					"as_of must not be later than decision_time")
			}
		}
	}

	return ScoringInput{
		ContractVersion: ContractVersion,
		DecisionTime:    decisionTime,
		PixPayment:      payment,
		Observations:    observations,
	}
}

func (d *decoder) pixPayment(root *strictjson.Value) PixPayment {
	object, path := d.ObjectMember("", root, "pix_payment", false,
		"amount_cents", "payer_device_usage", "recipient_key_usage", "recipient_person_type")
	if object == nil {
		return PixPayment{}
	}

	amount, _ := d.Integer(path, object, "amount_cents", 1, false)

	return PixPayment{
		AmountCents: amount,
		PayerDeviceUsage: PayerDeviceUsage(d.Enum(path, object, "payer_device_usage",
			string(PayerDevice), string(NoPayerDevice))),
		RecipientKeyUsage: RecipientKeyUsage(d.Enum(path, object, "recipient_key_usage",
			string(PixKey), string(NoPixKey))),
		RecipientPersonType: PersonType(d.Enum(path, object, "recipient_person_type",
			string(NaturalPerson), string(LegalPerson))),
	}
}

func (d *decoder) observations(root *strictjson.Value, payment PixPayment) Observations {
	object, path := d.ObjectMember("", root, "observations", false,
		"payer_account", "payer_device", "recipient_person", "recipient_account", "recipient_pix_key")
	if object == nil {
		return Observations{}
	}

	return Observations{
		PayerAccount: d.payerAccount(path, object),
		PayerDevice: conditionalSubject(d, path, object, "payer_device",
			"payer_device_usage", string(payment.PayerDeviceUsage), string(PayerDevice),
			d.payerDevice),
		RecipientPerson:  d.recipientPerson(path, object),
		RecipientAccount: d.recipientAccount(path, object),
		RecipientPixKey: conditionalSubject(d, path, object, "recipient_pix_key",
			"recipient_key_usage", string(payment.RecipientKeyUsage), string(PixKey),
			d.recipientPixKey),
	}
}

// conditionalSubject decodes a subject whose presence depends on a Pix
// payment discriminator. The subject is required when the discriminator has
// the applicable value and must be omitted otherwise. When the discriminator
// is invalid (""), presence is not checked.
func conditionalSubject[T any](
	d *decoder,
	path string,
	parent *strictjson.Value,
	name, discriminator, value, applicable string,
	decode func(string, *strictjson.Value) *T,
) *T {
	_, present := parent.Members[name]

	switch {
	case value == "" && !present:
		return nil
	case value != "" && value != applicable && present:
		d.Add(strictjson.Pointer(path, name), CodeMemberNotAllowed,
			"%s must be omitted when %s is %s", name, discriminator, value)
		return nil
	case value != "" && value != applicable:
		return nil
	}

	return decode(path, parent)
}

func (d *decoder) payerAccount(path string, parent *strictjson.Value) PayerAccount {
	object, subjectPath := d.ObjectMember(path, parent, "payer_account", false,
		"settled_outgoing_pix_90d", "initiated_outgoing_pix_24h")
	if object == nil {
		return PayerAccount{}
	}

	return PayerAccount{
		SettledOutgoingPix90d:   d.settledHistory(subjectPath, object, "settled_outgoing_pix_90d"),
		InitiatedOutgoingPix24h: d.countObservation(subjectPath, object, "initiated_outgoing_pix_24h"),
	}
}

func (d *decoder) payerDevice(path string, parent *strictjson.Value) *PayerDeviceSubject {
	object, subjectPath := d.ObjectMember(path, parent, "payer_device", false,
		"settled_outgoing_pix_90d")
	if object == nil {
		return nil
	}

	return &PayerDeviceSubject{
		SettledOutgoingPix90d: d.countObservation(subjectPath, object, "settled_outgoing_pix_90d"),
	}
}

func (d *decoder) recipientPerson(path string, parent *strictjson.Value) RecipientPerson {
	object, subjectPath := d.ObjectMember(path, parent, "recipient_person", false,
		"settled_pix_from_payer_account_365d", "spi", "fraud_markers", "infraction_reports", "entries")
	if object == nil {
		return RecipientPerson{}
	}

	return RecipientPerson{
		SettledPixFromPayerAccount365d: d.countObservation(subjectPath, object,
			"settled_pix_from_payer_account_365d"),
		Spi:               d.spi(subjectPath, object),
		FraudMarkers:      d.fraudMarkers(subjectPath, object),
		InfractionReports: d.infractionReports(subjectPath, object),
		Entries:           d.personEntries(subjectPath, object),
	}
}

func (d *decoder) recipientAccount(path string, parent *strictjson.Value) RecipientAccount {
	object, subjectPath := d.ObjectMember(path, parent, "recipient_account", false, "entry")
	if object == nil {
		return RecipientAccount{}
	}

	return RecipientAccount{Entry: d.accountEntry(subjectPath, object)}
}

func (d *decoder) recipientPixKey(path string, parent *strictjson.Value) *RecipientPixKey {
	object, subjectPath := d.ObjectMember(path, parent, "recipient_pix_key", false,
		"entry", "spi", "fraud_markers", "infraction_reports", "entries")
	if object == nil {
		return nil
	}

	return &RecipientPixKey{
		Entry:             d.keyEntry(subjectPath, object),
		Spi:               d.spi(subjectPath, object),
		FraudMarkers:      d.fraudMarkers(subjectPath, object),
		InfractionReports: d.infractionReports(subjectPath, object),
		Entries:           d.keyEntries(subjectPath, object),
	}
}

func (d *decoder) settledHistory(path string, parent *strictjson.Value, name string) *SettledHistory {
	object, observationPath := d.ObjectMember(path, parent, name, true,
		"count", "median_amount_cents", "as_of")
	if object == nil {
		return nil
	}

	count, countState := d.Integer(observationPath, object, "count", 0, false)
	median, medianState := d.Integer(observationPath, object, "median_amount_cents", 1, true)

	history := &SettledHistory{
		Count: count,
		AsOf:  d.asOfValue(observationPath, object),
	}

	if medianState == strictjson.Valid {
		history.MedianAmountCents = &median
	}

	if countState == strictjson.Valid && medianState != strictjson.Invalid &&
		(count == 0) != (medianState == strictjson.Null) {
		d.Add(strictjson.Pointer(observationPath, "median_amount_cents"), CodeMedianCountMismatch,
			"median_amount_cents must be null exactly when count is 0")
	}

	return history
}

func (d *decoder) countObservation(path string, parent *strictjson.Value, name string) *CountObservation {
	object, observationPath := d.ObjectMember(path, parent, name, true, "count", "as_of")
	if object == nil {
		return nil
	}

	count, _ := d.Integer(observationPath, object, "count", 0, false)

	return &CountObservation{
		Count: count,
		AsOf:  d.asOfValue(observationPath, object),
	}
}

func (d *decoder) accountEntry(path string, parent *strictjson.Value) *AccountEntry {
	object, observationPath := d.ObjectMember(path, parent, "entry", true, "opening_date", "as_of")
	if object == nil {
		return nil
	}

	openingDate, openingOK := d.Timestamp(observationPath, object, "opening_date")
	asOf, asOfOK := d.asOf(observationPath, object)

	if openingOK && asOfOK {
		d.notAfterAsOf(observationPath, "opening_date", openingDate, asOf)
	}

	return &AccountEntry{
		OpeningDate: openingDate,
		AsOf:        asOf,
	}
}

func (d *decoder) keyEntry(path string, parent *strictjson.Value) *KeyEntry {
	object, observationPath := d.ObjectMember(path, parent, "entry", true,
		"key_type", "creation_date", "key_ownership_date", "as_of")
	if object == nil {
		return nil
	}

	keyType := d.keyType(observationPath, object)
	creationDate, creationOK := d.Timestamp(observationPath, object, "creation_date")
	ownershipDate, ownershipOK := d.Timestamp(observationPath, object, "key_ownership_date")

	asOf, asOfOK := d.asOf(observationPath, object)

	if creationOK && asOfOK {
		d.notAfterAsOf(observationPath, "creation_date", creationDate, asOf)
	}

	if ownershipOK && asOfOK {
		d.notAfterAsOf(observationPath, "key_ownership_date", ownershipDate, asOf)
	}

	if creationOK && ownershipOK && ownershipDate.After(creationDate) {
		d.Add(strictjson.Pointer(observationPath, "key_ownership_date"), CodeKeyOwnershipAfterCreation,
			"key_ownership_date must not be later than creation_date")
	}

	return &KeyEntry{
		KeyType:          keyType,
		CreationDate:     creationDate,
		KeyOwnershipDate: ownershipDate,
		AsOf:             asOf,
	}
}

func (d *decoder) windowCounter(path string, parent *strictjson.Value, name string) *WindowCounter {
	object, counterPath := d.ObjectMember(path, parent, name, true, "d90", "m12", "m60")
	if object == nil {
		return nil
	}

	return &WindowCounter{
		D90: d.OptionalInteger(counterPath, object, "d90"),
		M12: d.OptionalInteger(counterPath, object, "m12"),
		M60: d.OptionalInteger(counterPath, object, "m60"),
	}
}

func (d *decoder) spi(path string, parent *strictjson.Value) *SpiStatistics {
	object, groupPath := d.ObjectMember(path, parent, "spi", true, "settlements", "as_of")
	if object == nil {
		return nil
	}

	return &SpiStatistics{
		Settlements: d.windowCounter(groupPath, object, "settlements"),
		AsOf:        d.asOfValue(groupPath, object),
	}
}

func (d *decoder) fraudMarkers(path string, parent *strictjson.Value) *FraudMarkers {
	object, groupPath := d.ObjectMember(path, parent, "fraud_markers", true,
		"application_frauds", "mule_accounts", "scammer_accounts",
		"other_frauds", "unknown_frauds", "distinct_fraud_reporters", "as_of")
	if object == nil {
		return nil
	}

	return &FraudMarkers{
		ApplicationFrauds:      d.windowCounter(groupPath, object, "application_frauds"),
		MuleAccounts:           d.windowCounter(groupPath, object, "mule_accounts"),
		ScammerAccounts:        d.windowCounter(groupPath, object, "scammer_accounts"),
		OtherFrauds:            d.windowCounter(groupPath, object, "other_frauds"),
		UnknownFrauds:          d.windowCounter(groupPath, object, "unknown_frauds"),
		DistinctFraudReporters: d.windowCounter(groupPath, object, "distinct_fraud_reporters"),
		AsOf:                   d.asOfValue(groupPath, object),
	}
}

func (d *decoder) infractionReports(path string, parent *strictjson.Value) *InfractionReports {
	object, groupPath := d.ObjectMember(path, parent, "infraction_reports", true,
		"open_reports", "open_reports_distinct_reporters", "rejected_reports", "as_of")
	if object == nil {
		return nil
	}

	return &InfractionReports{
		OpenReports:                  d.OptionalInteger(groupPath, object, "open_reports"),
		OpenReportsDistinctReporters: d.OptionalInteger(groupPath, object, "open_reports_distinct_reporters"),
		RejectedReports:              d.windowCounter(groupPath, object, "rejected_reports"),
		AsOf:                         d.asOfValue(groupPath, object),
	}
}

func (d *decoder) personEntries(path string, parent *strictjson.Value) *PersonEntries {
	object, groupPath := d.ObjectMember(path, parent, "entries", true, "registered_accounts", "as_of")
	if object == nil {
		return nil
	}

	return &PersonEntries{
		RegisteredAccounts: d.OptionalInteger(groupPath, object, "registered_accounts"),
		AsOf:               d.asOfValue(groupPath, object),
	}
}

func (d *decoder) keyEntries(path string, parent *strictjson.Value) *KeyEntries {
	object, groupPath := d.ObjectMember(path, parent, "entries", true, "distinct_accounts", "as_of")
	if object == nil {
		return nil
	}

	return &KeyEntries{
		DistinctAccounts: d.windowCounter(groupPath, object, "distinct_accounts"),
		AsOf:             d.asOfValue(groupPath, object),
	}
}

// isKeyType matches ^[A-Z][A-Z0-9_]{0,31}$.
func isKeyType(text string) bool {
	if len(text) < 1 || len(text) > 32 || text[0] < 'A' || text[0] > 'Z' {
		return false
	}

	for i := 1; i < len(text); i++ {
		c := text[i]
		if !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') && c != '_' {
			return false
		}
	}

	return true
}
