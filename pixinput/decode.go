package pixinput

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/digocelos/transaction-risk-go/internal/strictjson"
)

// Read reads one scoring input document and decodes it, accepting only the
// given contract versions. It stops reading as soon as the document exceeds
// MaxDocumentSize. A reader failure is returned as is; a document that
// breaks the contract yields a *ValidationError.
func Read(r io.Reader, versions ...string) (ScoringInput, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxDocumentSize+1))
	if err != nil {
		return ScoringInput{}, fmt.Errorf("read scoring input: %w", err)
	}

	return Decode(data, versions...)
}

// Decode validates a scoring input document and returns its typed form. It
// accepts only the given contract versions, so a document of any other
// version is rejected with unsupported_contract_version. A document that
// breaks the contract yields a *ValidationError.
func Decode(data []byte, versions ...string) (ScoringInput, error) {
	root, issues := parseDocument(data)
	if len(issues) > 0 {
		return ScoringInput{}, newValidationError(issues)
	}

	version, issue := checkContractVersion(root, versions)
	if issue != nil {
		return ScoringInput{}, newValidationError([]strictjson.Issue{*issue})
	}

	d := &decoder{v2: version == ContractV2}
	input := d.scoringInput(root)
	input.ContractVersion = version

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
// version's structure, and accepts it only if it is in versions and this
// package supports it.
func checkContractVersion(root *strictjson.Value, versions []string) (string, *strictjson.Issue) {
	const path = "/contract_version"

	value, ok := root.Members["contract_version"]

	switch {
	case !ok:
		return "", &strictjson.Issue{Path: path, Code: CodeRequired, Message: "contract_version is required"}
	case value.Kind == strictjson.KindNull:
		return "", &strictjson.Issue{Path: path, Code: CodeNullNotAllowed, Message: "contract_version must not be null"}
	case value.Kind != strictjson.KindString:
		return "", &strictjson.Issue{Path: path, Code: CodeWrongType, Message: "contract_version must be a string"}
	case !slices.Contains(versions, value.Text) || (value.Text != ContractV1 && value.Text != ContractV2):
		return "", &strictjson.Issue{
			Path:    path,
			Code:    CodeUnsupportedContractVersion,
			Message: fmt.Sprintf("unsupported contract version %q", value.Text),
		}
	}

	return value.Text, nil
}

// decoder walks a parsed document and collects issues. v2 selects the
// pix-payer-input-v2 structure.
type decoder struct {
	strictjson.Validator
	v2    bool
	asOfs []timedMember
}

// members returns the members of an object: those of version 1, plus those
// that version 2 adds when decoding version 2.
func (d *decoder) members(v1 []string, v2 ...string) []string {
	if d.v2 {
		return append(v1, v2...)
	}
	return v1
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
		DecisionTime: decisionTime,
		PixPayment:   payment,
		Observations: observations,
	}
}

func (d *decoder) pixPayment(root *strictjson.Value) PixPayment {
	object, path := d.ObjectMember("", root, "pix_payment", false, d.members(
		[]string{"amount_cents", "payer_device_usage", "recipient_key_usage", "recipient_person_type"},
		"reference_time_offset", "payer_limit_period")...)
	if object == nil {
		return PixPayment{}
	}

	amount, _ := d.Integer(path, object, "amount_cents", 1, false)

	payment := PixPayment{
		AmountCents: amount,
		PayerDeviceUsage: PayerDeviceUsage(d.Enum(path, object, "payer_device_usage",
			string(PayerDevice), string(NoPayerDevice))),
		RecipientKeyUsage: RecipientKeyUsage(d.Enum(path, object, "recipient_key_usage",
			string(PixKey), string(NoPixKey))),
		RecipientPersonType: PersonType(d.Enum(path, object, "recipient_person_type",
			string(NaturalPerson), string(LegalPerson))),
	}

	if d.v2 {
		payment.ReferenceTimeOffsetMinutes = d.referenceTimeOffset(path, object)
		payment.PayerLimitPeriod = LimitPeriod(d.Enum(path, object, "payer_limit_period",
			string(Daytime), string(Nighttime), string(Daily), string(NoPeriodLimit)))
	}

	return payment
}

// maxOffsetMinutes is the largest reference time offset, 14:00.
const maxOffsetMinutes = 14 * 60

// referenceTimeOffset reads a signed offset string ±hh:mm in minutes.
// -00:00 is an unknown local offset and is rejected.
func (d *decoder) referenceTimeOffset(path string, parent *strictjson.Value) int {
	const name = "reference_time_offset"

	value, memberPath, state := d.Member(path, parent, name, false)
	if state != strictjson.Valid {
		return 0
	}

	if value.Kind != strictjson.KindString {
		d.Add(memberPath, CodeWrongType, "%s must be a string", name)
		return 0
	}

	minutes, ok := parseOffset(value.Text)
	switch {
	case !ok:
		d.Add(memberPath, CodeInvalidFormat, "%s must match ^[+-][0-9]{2}:[0-9]{2}$ and must not be -00:00", name)
		return 0
	case minutes > maxOffsetMinutes || minutes < -maxOffsetMinutes:
		d.Add(memberPath, CodeOutOfRange, "%s must be from -14:00 to +14:00", name)
		return 0
	}

	return minutes
}

// parseOffset parses ±hh:mm with minutes from 00 to 59, rejecting -00:00.
func parseOffset(text string) (int, bool) {
	if len(text) != 6 || (text[0] != '+' && text[0] != '-') || text[3] != ':' || text == "-00:00" {
		return 0, false
	}

	for _, i := range []int{1, 2, 4, 5} {
		if text[i] < '0' || text[i] > '9' {
			return 0, false
		}
	}

	hours := int(text[1]-'0')*10 + int(text[2]-'0')
	minutes := int(text[4]-'0')*10 + int(text[5]-'0')
	if minutes > 59 {
		return 0, false
	}

	total := hours*60 + minutes
	if text[0] == '-' {
		total = -total
	}

	return total, true
}

func (d *decoder) observations(root *strictjson.Value, payment PixPayment) Observations {
	object, path := d.ObjectMember("", root, "observations", false,
		"payer_account", "payer_device", "recipient_person", "recipient_account", "recipient_pix_key")
	if object == nil {
		return Observations{}
	}

	return Observations{
		PayerAccount: d.payerAccount(path, object, payment),
		PayerDevice: conditionalMember(d, path, object, "payer_device",
			"payer_device_usage", string(payment.PayerDeviceUsage), payment.PayerDeviceUsage == PayerDevice,
			d.payerDevice),
		RecipientPerson:  d.recipientPerson(path, object),
		RecipientAccount: d.recipientAccount(path, object),
		RecipientPixKey: conditionalMember(d, path, object, "recipient_pix_key",
			"recipient_key_usage", string(payment.RecipientKeyUsage), payment.RecipientKeyUsage == PixKey,
			d.recipientPixKey),
	}
}

// conditionalMember decodes a subject or observation whose presence
// depends on a Pix payment discriminator. It is required when the
// discriminator's value makes it applicable and must be omitted otherwise.
// When the discriminator is invalid (""), presence is not checked.
func conditionalMember[T any](
	d *decoder,
	path string,
	parent *strictjson.Value,
	name, discriminator, value string,
	applies bool,
	decode func(string, *strictjson.Value) *T,
) *T {
	_, present := parent.Members[name]

	switch {
	case value == "" && !present:
		return nil
	case value != "" && !applies && present:
		d.Add(strictjson.Pointer(path, name), CodeMemberNotAllowed,
			"%s must be omitted when %s is %s", name, discriminator, value)
		return nil
	case value != "" && !applies:
		return nil
	}

	return decode(path, parent)
}

func (d *decoder) payerAccount(path string, parent *strictjson.Value, payment PixPayment) PayerAccount {
	object, subjectPath := d.ObjectMember(path, parent, "payer_account", false, d.members(
		[]string{"settled_outgoing_pix_90d", "initiated_outgoing_pix_24h"}, "period_limit")...)
	if object == nil {
		return PayerAccount{}
	}

	account := PayerAccount{
		SettledOutgoingPix90d:   d.settledHistory(subjectPath, object, "settled_outgoing_pix_90d"),
		InitiatedOutgoingPix24h: d.countObservation(subjectPath, object, "initiated_outgoing_pix_24h"),
	}

	if d.v2 {
		account.PeriodLimit = conditionalMember(d, subjectPath, object, "period_limit",
			"payer_limit_period", string(payment.PayerLimitPeriod), payment.PayerLimitPeriod != NoPeriodLimit,
			d.periodLimit)
	}

	return account
}

func (d *decoder) payerDevice(path string, parent *strictjson.Value) *PayerDeviceSubject {
	object, subjectPath := d.ObjectMember(path, parent, "payer_device", false, d.members(
		[]string{"settled_outgoing_pix_90d"}, "registration")...)
	if object == nil {
		return nil
	}

	device := &PayerDeviceSubject{
		SettledOutgoingPix90d: d.countObservation(subjectPath, object, "settled_outgoing_pix_90d"),
	}

	if d.v2 {
		device.Registration = d.registration(subjectPath, object, "registration")
	}

	return device
}

func (d *decoder) periodLimit(path string, parent *strictjson.Value) *PeriodLimit {
	object, observationPath := d.ObjectMember(path, parent, "period_limit", true,
		"limit_cents", "consumed_cents", "as_of")
	if object == nil {
		return nil
	}

	limit, _ := d.Integer(observationPath, object, "limit_cents", 0, false)
	consumed, _ := d.Integer(observationPath, object, "consumed_cents", 0, false)

	return &PeriodLimit{
		LimitCents:    limit,
		ConsumedCents: consumed,
		AsOf:          d.asOfValue(observationPath, object),
	}
}

func (d *decoder) registration(path string, parent *strictjson.Value, name string) *Registration {
	object, observationPath := d.ObjectMember(path, parent, name, true, "status", "as_of")
	if object == nil {
		return nil
	}

	return &Registration{
		Status: RegistrationStatus(d.Enum(observationPath, object, "status",
			string(Registered), string(NotRegistered))),
		AsOf: d.asOfValue(observationPath, object),
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
	object, subjectPath := d.ObjectMember(path, parent, "recipient_account", false, d.members(
		[]string{"entry"}, "pre_registration")...)
	if object == nil {
		return RecipientAccount{}
	}

	account := RecipientAccount{Entry: d.accountEntry(subjectPath, object)}

	if d.v2 {
		account.PreRegistration = d.registration(subjectPath, object, "pre_registration")
	}

	return account
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
