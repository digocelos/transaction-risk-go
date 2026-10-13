package pixinput

import (
	"cmp"
	"fmt"
	"slices"
)

// Validation codes. The catalog is closed; see the pix-payer-input-v1
// specification for the condition behind each code.
const (
	CodeDocumentTooLarge           = "document_too_large"
	CodeBOMNotAllowed              = "bom_not_allowed"
	CodeInvalidUTF8                = "invalid_utf8"
	CodeInvalidJSON                = "invalid_json"
	CodeTrailingData               = "trailing_data"
	CodeWrongType                  = "wrong_type"
	CodeDuplicateMember            = "duplicate_member"
	CodeRequired                   = "required"
	CodeUnsupportedContractVersion = "unsupported_contract_version"
	CodeUnknownMember              = "unknown_member"
	CodeMemberNotAllowed           = "member_not_allowed"
	CodeNullNotAllowed             = "null_not_allowed"
	CodeInvalidNumberFormat        = "invalid_number_format"
	CodeOutOfRange                 = "out_of_range"
	CodeInvalidFormat              = "invalid_format"
	CodeInvalidEnumValue           = "invalid_enum_value"
	CodeAsOfAfterDecisionTime      = "as_of_after_decision_time"
	CodeMedianCountMismatch        = "median_count_mismatch"
	CodeDateAfterAsOf              = "date_after_as_of"
	CodeKeyOwnershipAfterCreation  = "key_ownership_after_creation"
)

// Issue is one reason why a document is not a valid scoring input.
// Path is a JSON Pointer; Message is for humans and may change.
type Issue struct {
	Path    string
	Code    string
	Message string
}

// ValidationError lists every issue found in a scoring input document,
// sorted by path and then by code.
type ValidationError struct {
	Issues []Issue
}

func (e *ValidationError) Error() string {
	first := e.Issues[0]
	message := fmt.Sprintf("invalid scoring input: %s: %s", first.Code, first.Message)

	if len(e.Issues) > 1 {
		message += fmt.Sprintf(" (and %d more issues)", len(e.Issues)-1)
	}

	return message
}

// newValidationError sorts issues by the bytes of their path and code,
// keeping one issue per path and code.
func newValidationError(issues []Issue) *ValidationError {
	sorted := slices.Clone(issues)

	slices.SortStableFunc(sorted, func(a, b Issue) int {
		return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Code, b.Code))
	})

	sorted = slices.CompactFunc(sorted, func(a, b Issue) bool {
		return a.Path == b.Path && a.Code == b.Code
	})

	return &ValidationError{Issues: sorted}
}
