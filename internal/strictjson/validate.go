package strictjson

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Codes shared by every input contract that this package validates.
const (
	CodeBOMNotAllowed       = "bom_not_allowed"
	CodeInvalidUTF8         = "invalid_utf8"
	CodeInvalidJSON         = "invalid_json"
	CodeTrailingData        = "trailing_data"
	CodeWrongType           = "wrong_type"
	CodeDuplicateMember     = "duplicate_member"
	CodeRequired            = "required"
	CodeUnknownMember       = "unknown_member"
	CodeNullNotAllowed      = "null_not_allowed"
	CodeInvalidNumberFormat = "invalid_number_format"
	CodeOutOfRange          = "out_of_range"
	CodeInvalidFormat       = "invalid_format"
	CodeInvalidEnumValue    = "invalid_enum_value"
)

// MaxInteger is the largest integer any field accepts, 2^53−1.
const MaxInteger = 1<<53 - 1

// Issue is one problem found while validating a document.
type Issue struct {
	Path    string
	Code    string
	Message string
}

// SortIssues sorts issues by the bytes of their path and code, keeping one
// issue per path and code.
func SortIssues(issues []Issue) []Issue {
	sorted := slices.Clone(issues)

	slices.SortStableFunc(sorted, func(a, b Issue) int {
		return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Code, b.Code))
	})

	return slices.CompactFunc(sorted, func(a, b Issue) bool {
		return a.Path == b.Path && a.Code == b.Code
	})
}

// State tells whether a decoded member was valid, null, or invalid.
type State int

const (
	Invalid State = iota
	Null
	Valid
)

// Validator reads members of a parsed document and collects issues.
type Validator struct {
	issues []Issue
}

// Issues returns the collected issues in the order they were found.
func (v *Validator) Issues() []Issue {
	return v.issues
}

// Add records an issue.
func (v *Validator) Add(path, code, format string, args ...any) {
	v.issues = append(v.issues, Issue{
		Path:    path,
		Code:    code,
		Message: fmt.Sprintf(format, args...),
	})
}

// Object reports every member of value that is not in members.
func (v *Validator) Object(path string, value *Value, members ...string) {
	for _, name := range value.Names {
		if !slices.Contains(members, name) {
			v.Add(Pointer(path, name), CodeUnknownMember,
				"%s is not a member of this object", name)
		}
	}
}

// Member returns a required member, reporting it when absent or null.
// A nullable member that is null returns Null.
func (v *Validator) Member(path string, parent *Value, name string, nullable bool) (*Value, string, State) {
	memberPath := Pointer(path, name)

	value, ok := parent.Members[name]
	switch {
	case !ok:
		v.Add(memberPath, CodeRequired, "%s is required", name)
		return nil, memberPath, Invalid
	case value.Kind == KindNull && nullable:
		return nil, memberPath, Null
	case value.Kind == KindNull:
		v.Add(memberPath, CodeNullNotAllowed, "%s must not be null", name)
		return nil, memberPath, Invalid
	}

	return value, memberPath, Valid
}

// ObjectMember returns a member that must be an object with only the given
// members. It returns nil when the member is null, absent, or invalid.
func (v *Validator) ObjectMember(path string, parent *Value, name string, nullable bool, members ...string) (*Value, string) {
	value, memberPath, state := v.Member(path, parent, name, nullable)
	if state != Valid {
		return nil, memberPath
	}

	if value.Kind != KindObject {
		v.Add(memberPath, CodeWrongType, "%s must be an object", name)
		return nil, memberPath
	}

	v.Object(memberPath, value, members...)

	return value, memberPath
}

// Integer reads an unsigned integer member with no fraction or exponent,
// from minimum to MaxInteger.
func (v *Validator) Integer(path string, parent *Value, name string, minimum int64, nullable bool) (int64, State) {
	value, memberPath, state := v.Member(path, parent, name, nullable)
	if state != Valid {
		return 0, state
	}

	if value.Kind != KindNumber {
		v.Add(memberPath, CodeWrongType, "%s must be an integer", name)
		return 0, Invalid
	}

	if !IsUnsignedInteger(value.Text) {
		v.Add(memberPath, CodeInvalidNumberFormat,
			"%s must be an unsigned integer without fraction or exponent", name)
		return 0, Invalid
	}

	number, err := strconv.ParseInt(value.Text, 10, 64)
	if err != nil || number > MaxInteger {
		v.Add(memberPath, CodeOutOfRange, "%s must be at most %d", name, int64(MaxInteger))
		return 0, Invalid
	}

	if number < minimum {
		v.Add(memberPath, CodeOutOfRange, "%s must be at least %d", name, minimum)
		return 0, Invalid
	}

	return number, Valid
}

// OptionalInteger reads a nullable integer member from 0 as a pointer.
func (v *Validator) OptionalInteger(path string, parent *Value, name string) *int64 {
	number, state := v.Integer(path, parent, name, 0, true)
	if state != Valid {
		return nil
	}
	return &number
}

// Timestamp reads a non-nullable strict RFC 3339 timestamp member.
func (v *Validator) Timestamp(path string, parent *Value, name string) (time.Time, bool) {
	value, memberPath, state := v.Member(path, parent, name, false)
	if state != Valid {
		return time.Time{}, false
	}

	if value.Kind != KindString {
		v.Add(memberPath, CodeWrongType, "%s must be a string", name)
		return time.Time{}, false
	}

	parsed, ok := ParseTimestamp(value.Text)
	if !ok {
		v.Add(memberPath, CodeInvalidFormat,
			"%s must be an RFC 3339 timestamp with an explicit offset", name)
		return time.Time{}, false
	}

	return parsed, true
}

// Enum reads a non-nullable closed enumeration member. It returns "" when
// the member is invalid.
func (v *Validator) Enum(path string, parent *Value, name string, values ...string) string {
	value, memberPath, state := v.Member(path, parent, name, false)
	if state != Valid {
		return ""
	}

	if value.Kind != KindString {
		v.Add(memberPath, CodeWrongType, "%s must be a string", name)
		return ""
	}

	if !slices.Contains(values, value.Text) {
		v.Add(memberPath, CodeInvalidEnumValue,
			"%s must be one of %s", name, strings.Join(values, ", "))
		return ""
	}

	return value.Text
}

// String reads a string member whose syntax is checked by match, which is
// described by syntax in messages. The value is preserved exactly.
func (v *Validator) String(path string, parent *Value, name string, nullable bool, match func(string) bool, syntax string) (string, State) {
	value, memberPath, state := v.Member(path, parent, name, nullable)
	if state != Valid {
		return "", state
	}

	if value.Kind != KindString {
		v.Add(memberPath, CodeWrongType, "%s must be a string", name)
		return "", Invalid
	}

	if !match(value.Text) {
		v.Add(memberPath, CodeInvalidFormat, "%s must match %s", name, syntax)
		return "", Invalid
	}

	return value.Text, Valid
}

// IsUnsignedInteger matches ^(0|[1-9][0-9]*)$.
func IsUnsignedInteger(text string) bool {
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
