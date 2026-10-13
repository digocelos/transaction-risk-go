package pixinput

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// maxInteger is the largest integer any field accepts, 2^53−1.
const maxInteger = 1<<53 - 1

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
		return ScoringInput{}, newValidationError([]Issue{*issue})
	}

	d := &decoder{}
	input := d.scoringInput(root)

	if len(d.issues) > 0 {
		return ScoringInput{}, newValidationError(d.issues)
	}

	return input, nil
}

// parseDocument applies the document rules. When it returns issues,
// validation stops: a document with duplicate members is ambiguous.
func parseDocument(data []byte) (*jsonValue, []Issue) {
	switch {
	case len(data) > MaxDocumentSize:
		return nil, []Issue{{
			Path:    "",
			Code:    CodeDocumentTooLarge,
			Message: fmt.Sprintf("document exceeds %d bytes", MaxDocumentSize),
		}}
	case bytes.HasPrefix(data, []byte("\xEF\xBB\xBF")):
		return nil, []Issue{{
			Path:    "",
			Code:    CodeBOMNotAllowed,
			Message: "document must not start with a byte order mark",
		}}
	case !utf8.Valid(data):
		return nil, []Issue{{Path: "", Code: CodeInvalidUTF8, Message: "document is not valid UTF-8"}}
	}

	root, duplicates, err := parseJSON(data)

	switch {
	case errors.Is(err, errTrailingData):
		return nil, []Issue{{
			Path:    "",
			Code:    CodeTrailingData,
			Message: "only JSON whitespace may follow the top-level value",
		}}
	case err != nil:
		return nil, []Issue{{Path: "", Code: CodeInvalidJSON, Message: "document is not valid JSON"}}
	case root.kind != kindObject:
		return nil, []Issue{{Path: "", Code: CodeWrongType, Message: "document must be a JSON object"}}
	}

	var issues []Issue
	for _, path := range duplicates {
		issues = append(issues, Issue{
			Path:    path,
			Code:    CodeDuplicateMember,
			Message: "member appears more than once in the same object",
		})
	}

	return root, issues
}

// checkContractVersion locates the contract version without assuming any
// version's structure.
func checkContractVersion(root *jsonValue) *Issue {
	const path = "/contract_version"

	value, ok := root.members["contract_version"]

	switch {
	case !ok:
		return &Issue{Path: path, Code: CodeRequired, Message: "contract_version is required"}
	case value.kind == kindNull:
		return &Issue{Path: path, Code: CodeNullNotAllowed, Message: "contract_version must not be null"}
	case value.kind != kindString:
		return &Issue{Path: path, Code: CodeWrongType, Message: "contract_version must be a string"}
	case value.text != ContractVersion:
		return &Issue{
			Path:    path,
			Code:    CodeUnsupportedContractVersion,
			Message: fmt.Sprintf("unsupported contract version %q", value.text),
		}
	}

	return nil
}

// valueState tells whether a decoded member was valid, null, or invalid.
type valueState int

const (
	stateInvalid valueState = iota
	stateNull
	stateValid
)

// decoder walks a parsed document and collects issues.
type decoder struct {
	issues []Issue
	asOfs  []timedMember
}

// timedMember is a valid as_of value and its path.
type timedMember struct {
	path string
	time time.Time
}

func (d *decoder) add(path, code, format string, args ...any) {
	d.issues = append(d.issues, Issue{
		Path:    path,
		Code:    code,
		Message: fmt.Sprintf(format, args...),
	})
}

// object reports every member of value that is not in members.
func (d *decoder) object(path string, value *jsonValue, members ...string) {
	for _, name := range value.names {
		if !slices.Contains(members, name) {
			d.add(pointer(path, name), CodeUnknownMember,
				"%s is not a member of this object", name)
		}
	}
}

// member returns a required member, reporting it when absent or null.
// A nullable member that is null returns stateNull.
func (d *decoder) member(path string, parent *jsonValue, name string, nullable bool) (*jsonValue, string, valueState) {
	memberPath := pointer(path, name)

	value, ok := parent.members[name]
	switch {
	case !ok:
		d.add(memberPath, CodeRequired, "%s is required", name)
		return nil, memberPath, stateInvalid
	case value.kind == kindNull && nullable:
		return nil, memberPath, stateNull
	case value.kind == kindNull:
		d.add(memberPath, CodeNullNotAllowed, "%s must not be null", name)
		return nil, memberPath, stateInvalid
	}

	return value, memberPath, stateValid
}

// objectMember returns a member that must be an object with only the given
// members. It returns nil when the member is null, absent, or invalid.
func (d *decoder) objectMember(path string, parent *jsonValue, name string, nullable bool, members ...string) (*jsonValue, string) {
	value, memberPath, state := d.member(path, parent, name, nullable)
	if state != stateValid {
		return nil, memberPath
	}

	if value.kind != kindObject {
		d.add(memberPath, CodeWrongType, "%s must be an object", name)
		return nil, memberPath
	}

	d.object(memberPath, value, members...)

	return value, memberPath
}

// integer reads an unsigned integer member with no fraction or exponent.
func (d *decoder) integer(path string, parent *jsonValue, name string, minimum int64, nullable bool) (int64, valueState) {
	value, memberPath, state := d.member(path, parent, name, nullable)
	if state != stateValid {
		return 0, state
	}

	if value.kind != kindNumber {
		d.add(memberPath, CodeWrongType, "%s must be an integer", name)
		return 0, stateInvalid
	}

	if !isUnsignedInteger(value.text) {
		d.add(memberPath, CodeInvalidNumberFormat,
			"%s must be an unsigned integer without fraction or exponent", name)
		return 0, stateInvalid
	}

	number, err := strconv.ParseInt(value.text, 10, 64)
	if err != nil || number > maxInteger {
		d.add(memberPath, CodeOutOfRange, "%s must be at most %d", name, int64(maxInteger))
		return 0, stateInvalid
	}

	if number < minimum {
		d.add(memberPath, CodeOutOfRange, "%s must be at least %d", name, minimum)
		return 0, stateInvalid
	}

	return number, stateValid
}

// optionalInteger reads a nullable integer member as a pointer.
func (d *decoder) optionalInteger(path string, parent *jsonValue, name string) *int64 {
	number, state := d.integer(path, parent, name, 0, true)
	if state != stateValid {
		return nil
	}
	return &number
}

// timestamp reads a non-nullable strict RFC 3339 timestamp member.
func (d *decoder) timestamp(path string, parent *jsonValue, name string) (time.Time, bool) {
	value, memberPath, state := d.member(path, parent, name, false)
	if state != stateValid {
		return time.Time{}, false
	}

	if value.kind != kindString {
		d.add(memberPath, CodeWrongType, "%s must be a string", name)
		return time.Time{}, false
	}

	parsed, ok := parseTimestamp(value.text)
	if !ok {
		d.add(memberPath, CodeInvalidFormat,
			"%s must be an RFC 3339 timestamp with an explicit offset", name)
		return time.Time{}, false
	}

	return parsed, true
}

// enum reads a non-nullable closed enumeration member. It returns "" when
// the member is invalid.
func (d *decoder) enum(path string, parent *jsonValue, name string, values ...string) string {
	value, memberPath, state := d.member(path, parent, name, false)
	if state != stateValid {
		return ""
	}

	if value.kind != kindString {
		d.add(memberPath, CodeWrongType, "%s must be a string", name)
		return ""
	}

	if !slices.Contains(values, value.text) {
		d.add(memberPath, CodeInvalidEnumValue,
			"%s must be one of %s", name, strings.Join(values, ", "))
		return ""
	}

	return value.text
}

// keyType reads the open string key_type, preserving it exactly.
func (d *decoder) keyType(path string, parent *jsonValue) string {
	value, memberPath, state := d.member(path, parent, "key_type", false)
	if state != stateValid {
		return ""
	}

	if value.kind != kindString {
		d.add(memberPath, CodeWrongType, "key_type must be a string")
		return ""
	}

	if !isKeyType(value.text) {
		d.add(memberPath, CodeInvalidFormat, "key_type must match ^[A-Z][A-Z0-9_]{0,31}$")
		return ""
	}

	return value.text
}

// asOf reads an observation's as_of and records it for the check against
// decision_time.
func (d *decoder) asOf(path string, parent *jsonValue) (time.Time, bool) {
	asOf, ok := d.timestamp(path, parent, "as_of")
	if ok {
		d.asOfs = append(d.asOfs, timedMember{pointer(path, "as_of"), asOf})
	}
	return asOf, ok
}

// asOfValue reads an observation's as_of when no other rule needs it.
func (d *decoder) asOfValue(path string, parent *jsonValue) time.Time {
	asOf, _ := d.asOf(path, parent)
	return asOf
}

// notAfterAsOf reports a date of an observation that is later than its as_of.
func (d *decoder) notAfterAsOf(path, name string, date, asOf time.Time) {
	if date.After(asOf) {
		d.add(pointer(path, name), CodeDateAfterAsOf, "%s must not be later than as_of", name)
	}
}

func (d *decoder) scoringInput(root *jsonValue) ScoringInput {
	d.object("", root, "contract_version", "decision_time", "pix_payment", "observations")

	decisionTime, decisionOK := d.timestamp("", root, "decision_time")
	payment := d.pixPayment(root)
	observations := d.observations(root, payment)

	if decisionOK {
		for _, asOf := range d.asOfs {
			if asOf.time.After(decisionTime) {
				d.add(asOf.path, CodeAsOfAfterDecisionTime,
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

func (d *decoder) pixPayment(root *jsonValue) PixPayment {
	object, path := d.objectMember("", root, "pix_payment", false,
		"amount_cents", "payer_device_usage", "recipient_key_usage", "recipient_person_type")
	if object == nil {
		return PixPayment{}
	}

	amount, _ := d.integer(path, object, "amount_cents", 1, false)

	return PixPayment{
		AmountCents: amount,
		PayerDeviceUsage: PayerDeviceUsage(d.enum(path, object, "payer_device_usage",
			string(PayerDevice), string(NoPayerDevice))),
		RecipientKeyUsage: RecipientKeyUsage(d.enum(path, object, "recipient_key_usage",
			string(PixKey), string(NoPixKey))),
		RecipientPersonType: PersonType(d.enum(path, object, "recipient_person_type",
			string(NaturalPerson), string(LegalPerson))),
	}
}

func (d *decoder) observations(root *jsonValue, payment PixPayment) Observations {
	object, path := d.objectMember("", root, "observations", false,
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
	parent *jsonValue,
	name, discriminator, value, applicable string,
	decode func(string, *jsonValue) *T,
) *T {
	_, present := parent.members[name]

	switch {
	case value == "" && !present:
		return nil
	case value != "" && value != applicable && present:
		d.add(pointer(path, name), CodeMemberNotAllowed,
			"%s must be omitted when %s is %s", name, discriminator, value)
		return nil
	case value != "" && value != applicable:
		return nil
	}

	return decode(path, parent)
}

func (d *decoder) payerAccount(path string, parent *jsonValue) PayerAccount {
	object, subjectPath := d.objectMember(path, parent, "payer_account", false,
		"settled_outgoing_pix_90d", "initiated_outgoing_pix_24h")
	if object == nil {
		return PayerAccount{}
	}

	return PayerAccount{
		SettledOutgoingPix90d:   d.settledHistory(subjectPath, object, "settled_outgoing_pix_90d"),
		InitiatedOutgoingPix24h: d.countObservation(subjectPath, object, "initiated_outgoing_pix_24h"),
	}
}

func (d *decoder) payerDevice(path string, parent *jsonValue) *PayerDeviceSubject {
	object, subjectPath := d.objectMember(path, parent, "payer_device", false,
		"settled_outgoing_pix_90d")
	if object == nil {
		return nil
	}

	return &PayerDeviceSubject{
		SettledOutgoingPix90d: d.countObservation(subjectPath, object, "settled_outgoing_pix_90d"),
	}
}

func (d *decoder) recipientPerson(path string, parent *jsonValue) RecipientPerson {
	object, subjectPath := d.objectMember(path, parent, "recipient_person", false,
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

func (d *decoder) recipientAccount(path string, parent *jsonValue) RecipientAccount {
	object, subjectPath := d.objectMember(path, parent, "recipient_account", false, "entry")
	if object == nil {
		return RecipientAccount{}
	}

	return RecipientAccount{Entry: d.accountEntry(subjectPath, object)}
}

func (d *decoder) recipientPixKey(path string, parent *jsonValue) *RecipientPixKey {
	object, subjectPath := d.objectMember(path, parent, "recipient_pix_key", false,
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

func (d *decoder) settledHistory(path string, parent *jsonValue, name string) *SettledHistory {
	object, observationPath := d.objectMember(path, parent, name, true,
		"count", "median_amount_cents", "as_of")
	if object == nil {
		return nil
	}

	count, countState := d.integer(observationPath, object, "count", 0, false)
	median, medianState := d.integer(observationPath, object, "median_amount_cents", 1, true)

	history := &SettledHistory{
		Count: count,
		AsOf:  d.asOfValue(observationPath, object),
	}

	if medianState == stateValid {
		history.MedianAmountCents = &median
	}

	if countState == stateValid && medianState != stateInvalid &&
		(count == 0) != (medianState == stateNull) {
		d.add(pointer(observationPath, "median_amount_cents"), CodeMedianCountMismatch,
			"median_amount_cents must be null exactly when count is 0")
	}

	return history
}

func (d *decoder) countObservation(path string, parent *jsonValue, name string) *CountObservation {
	object, observationPath := d.objectMember(path, parent, name, true, "count", "as_of")
	if object == nil {
		return nil
	}

	count, _ := d.integer(observationPath, object, "count", 0, false)

	return &CountObservation{
		Count: count,
		AsOf:  d.asOfValue(observationPath, object),
	}
}

func (d *decoder) accountEntry(path string, parent *jsonValue) *AccountEntry {
	object, observationPath := d.objectMember(path, parent, "entry", true, "opening_date", "as_of")
	if object == nil {
		return nil
	}

	openingDate, openingOK := d.timestamp(observationPath, object, "opening_date")
	asOf, asOfOK := d.asOf(observationPath, object)

	if openingOK && asOfOK {
		d.notAfterAsOf(observationPath, "opening_date", openingDate, asOf)
	}

	return &AccountEntry{
		OpeningDate: openingDate,
		AsOf:        asOf,
	}
}

func (d *decoder) keyEntry(path string, parent *jsonValue) *KeyEntry {
	object, observationPath := d.objectMember(path, parent, "entry", true,
		"key_type", "creation_date", "key_ownership_date", "as_of")
	if object == nil {
		return nil
	}

	keyType := d.keyType(observationPath, object)
	creationDate, creationOK := d.timestamp(observationPath, object, "creation_date")
	ownershipDate, ownershipOK := d.timestamp(observationPath, object, "key_ownership_date")

	asOf, asOfOK := d.asOf(observationPath, object)

	if creationOK && asOfOK {
		d.notAfterAsOf(observationPath, "creation_date", creationDate, asOf)
	}

	if ownershipOK && asOfOK {
		d.notAfterAsOf(observationPath, "key_ownership_date", ownershipDate, asOf)
	}

	if creationOK && ownershipOK && ownershipDate.After(creationDate) {
		d.add(pointer(observationPath, "key_ownership_date"), CodeKeyOwnershipAfterCreation,
			"key_ownership_date must not be later than creation_date")
	}

	return &KeyEntry{
		KeyType:          keyType,
		CreationDate:     creationDate,
		KeyOwnershipDate: ownershipDate,
		AsOf:             asOf,
	}
}

func (d *decoder) windowCounter(path string, parent *jsonValue, name string) *WindowCounter {
	object, counterPath := d.objectMember(path, parent, name, true, "d90", "m12", "m60")
	if object == nil {
		return nil
	}

	return &WindowCounter{
		D90: d.optionalInteger(counterPath, object, "d90"),
		M12: d.optionalInteger(counterPath, object, "m12"),
		M60: d.optionalInteger(counterPath, object, "m60"),
	}
}

func (d *decoder) spi(path string, parent *jsonValue) *SpiStatistics {
	object, groupPath := d.objectMember(path, parent, "spi", true, "settlements", "as_of")
	if object == nil {
		return nil
	}

	return &SpiStatistics{
		Settlements: d.windowCounter(groupPath, object, "settlements"),
		AsOf:        d.asOfValue(groupPath, object),
	}
}

func (d *decoder) fraudMarkers(path string, parent *jsonValue) *FraudMarkers {
	object, groupPath := d.objectMember(path, parent, "fraud_markers", true,
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

func (d *decoder) infractionReports(path string, parent *jsonValue) *InfractionReports {
	object, groupPath := d.objectMember(path, parent, "infraction_reports", true,
		"open_reports", "open_reports_distinct_reporters", "rejected_reports", "as_of")
	if object == nil {
		return nil
	}

	return &InfractionReports{
		OpenReports:                  d.optionalInteger(groupPath, object, "open_reports"),
		OpenReportsDistinctReporters: d.optionalInteger(groupPath, object, "open_reports_distinct_reporters"),
		RejectedReports:              d.windowCounter(groupPath, object, "rejected_reports"),
		AsOf:                         d.asOfValue(groupPath, object),
	}
}

func (d *decoder) personEntries(path string, parent *jsonValue) *PersonEntries {
	object, groupPath := d.objectMember(path, parent, "entries", true, "registered_accounts", "as_of")
	if object == nil {
		return nil
	}

	return &PersonEntries{
		RegisteredAccounts: d.optionalInteger(groupPath, object, "registered_accounts"),
		AsOf:               d.asOfValue(groupPath, object),
	}
}

func (d *decoder) keyEntries(path string, parent *jsonValue) *KeyEntries {
	object, groupPath := d.objectMember(path, parent, "entries", true, "distinct_accounts", "as_of")
	if object == nil {
		return nil
	}

	return &KeyEntries{
		DistinctAccounts: d.windowCounter(groupPath, object, "distinct_accounts"),
		AsOf:             d.asOfValue(groupPath, object),
	}
}

// isUnsignedInteger matches ^(0|[1-9][0-9]*)$.
func isUnsignedInteger(text string) bool {
	if text == "0" {
		return true
	}

	if text == "" || text[0] < '1' || text[0] > '9' {
		return false
	}

	for i := 1; i < len(text); i++ {
		if !isDigit(text[i]) {
			return false
		}
	}

	return true
}

// isKeyType matches ^[A-Z][A-Z0-9_]{0,31}$.
func isKeyType(text string) bool {
	if len(text) < 1 || len(text) > 32 || text[0] < 'A' || text[0] > 'Z' {
		return false
	}

	for i := 1; i < len(text); i++ {
		c := text[i]
		if !(c >= 'A' && c <= 'Z') && !isDigit(c) && c != '_' {
			return false
		}
	}

	return true
}
