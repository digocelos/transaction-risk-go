package pixinput

import (
	"fmt"

	"github.com/digocelos/transaction-risk-go/internal/strictjson"
)

// Validation codes. The catalog is closed; see the pix-payer-input-v1
// specification for the condition behind each code.
const (
	CodeDocumentTooLarge           = "document_too_large"
	CodeBOMNotAllowed              = strictjson.CodeBOMNotAllowed
	CodeInvalidUTF8                = strictjson.CodeInvalidUTF8
	CodeInvalidJSON                = strictjson.CodeInvalidJSON
	CodeTrailingData               = strictjson.CodeTrailingData
	CodeWrongType                  = strictjson.CodeWrongType
	CodeDuplicateMember            = strictjson.CodeDuplicateMember
	CodeRequired                   = strictjson.CodeRequired
	CodeUnsupportedContractVersion = "unsupported_contract_version"
	CodeUnknownMember              = strictjson.CodeUnknownMember
	CodeMemberNotAllowed           = "member_not_allowed"
	CodeNullNotAllowed             = strictjson.CodeNullNotAllowed
	CodeInvalidNumberFormat        = strictjson.CodeInvalidNumberFormat
	CodeOutOfRange                 = strictjson.CodeOutOfRange
	CodeInvalidFormat              = strictjson.CodeInvalidFormat
	CodeInvalidEnumValue           = strictjson.CodeInvalidEnumValue
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
func newValidationError(issues []strictjson.Issue) *ValidationError {
	sorted := strictjson.SortIssues(issues)

	converted := make([]Issue, len(sorted))
	for i, issue := range sorted {
		converted[i] = Issue(issue)
	}

	return &ValidationError{Issues: converted}
}
