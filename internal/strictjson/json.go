// Package strictjson parses JSON strictly and validates decoded values,
// collecting issues with JSON Pointer paths. It backs the input contracts.
package strictjson

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Kind identifies the type of a parsed JSON value.
type Kind int

const (
	KindNull Kind = iota
	KindBool
	KindNumber
	KindString
	KindArray
	KindObject
)

// Value is a parsed JSON value. Numbers keep their raw token, Items holds
// the elements of an array in order, and Start and End delimit the value's
// exact bytes in the parsed document.
type Value struct {
	Kind    Kind
	Text    string
	Members map[string]*Value
	Names   []string
	Items   []*Value
	Start   int
	End     int
}

var (
	ErrInvalidJSON  = errors.New("invalid JSON")
	ErrTrailingData = errors.New("trailing data")
)

var pointerEscaper = strings.NewReplacer("~", "~0", "/", "~1")

// Pointer appends an escaped member name to a JSON Pointer.
func Pointer(parent, name string) string {
	return parent + "/" + pointerEscaper.Replace(name)
}

// jsonParser parses RFC 8259 JSON and records repeated member names.
// The standard decoder cannot be used here: it keeps the last duplicate
// member and silently replaces unpaired surrogate escapes.
type jsonParser struct {
	data       []byte
	pos        int
	segments   []string
	duplicates []string
}

// Parse parses one JSON value and returns it with the JSON Pointers of
// repeated members. It expects valid UTF-8.
func Parse(data []byte) (*Value, []string, error) {
	p := &jsonParser{data: data}

	p.skipSpace()
	if p.pos == len(p.data) {
		return nil, nil, ErrInvalidJSON
	}

	value, err := p.value()
	if err != nil {
		return nil, nil, err
	}

	p.skipSpace()
	if p.pos != len(p.data) {
		return nil, nil, ErrTrailingData
	}

	return value, p.duplicates, nil
}

func (p *jsonParser) skipSpace() {
	for p.pos < len(p.data) {
		switch p.data[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

// value parses one value and records its byte span.
func (p *jsonParser) value() (*Value, error) {
	p.skipSpace()
	if p.pos == len(p.data) {
		return nil, ErrInvalidJSON
	}

	start := p.pos

	value, err := p.valueAt()
	if err != nil {
		return nil, err
	}

	value.Start, value.End = start, p.pos

	return value, nil
}

func (p *jsonParser) valueAt() (*Value, error) {
	switch c := p.data[p.pos]; {
	case c == '{':
		return p.object()
	case c == '[':
		return p.array()
	case c == '"':
		text, err := p.string()
		if err != nil {
			return nil, err
		}
		return &Value{Kind: KindString, Text: text}, nil
	case c == 't':
		return p.literal("true", KindBool)
	case c == 'f':
		return p.literal("false", KindBool)
	case c == 'n':
		return p.literal("null", KindNull)
	case c == '-' || isDigit(c):
		return p.number()
	default:
		return nil, ErrInvalidJSON
	}
}

func (p *jsonParser) object() (*Value, error) {
	object := &Value{Kind: KindObject, Members: map[string]*Value{}}
	p.pos++

	p.skipSpace()
	if p.consume('}') {
		return object, nil
	}

	for {
		p.skipSpace()
		if p.pos == len(p.data) || p.data[p.pos] != '"' {
			return nil, ErrInvalidJSON
		}

		name, err := p.string()
		if err != nil {
			return nil, err
		}

		p.skipSpace()
		if !p.consume(':') {
			return nil, ErrInvalidJSON
		}

		member, err := p.child(name)
		if err != nil {
			return nil, err
		}

		if _, exists := object.Members[name]; exists {
			p.duplicates = append(p.duplicates, p.pointer(name))
		} else {
			object.Members[name] = member
			object.Names = append(object.Names, name)
		}

		p.skipSpace()
		if p.consume('}') {
			return object, nil
		}

		if !p.consume(',') {
			return nil, ErrInvalidJSON
		}
	}
}

func (p *jsonParser) array() (*Value, error) {
	p.pos++

	array := &Value{Kind: KindArray}

	p.skipSpace()
	if p.consume(']') {
		return array, nil
	}

	for index := 0; ; index++ {
		item, err := p.child(strconv.Itoa(index))
		if err != nil {
			return nil, err
		}
		array.Items = append(array.Items, item)

		p.skipSpace()
		if p.consume(']') {
			return array, nil
		}

		if !p.consume(',') {
			return nil, ErrInvalidJSON
		}
	}
}

// child parses the value of a member or array element. The path of the
// value is kept as segments and built only when a duplicate needs it, so
// deep nesting stays linear.
func (p *jsonParser) child(segment string) (*Value, error) {
	p.segments = append(p.segments, segment)
	value, err := p.value()
	p.segments = p.segments[:len(p.segments)-1]

	return value, err
}

// pointer returns the JSON Pointer of member name in the current object.
func (p *jsonParser) pointer(name string) string {
	path := ""
	for _, segment := range p.segments {
		path = Pointer(path, segment)
	}

	return Pointer(path, name)
}

func (p *jsonParser) string() (string, error) {
	p.pos++

	var text []byte

	for p.pos < len(p.data) {
		c := p.data[p.pos]

		switch {
		case c == '"':
			p.pos++
			return string(text), nil
		case c < 0x20:
			return "", ErrInvalidJSON
		case c != '\\':
			text = append(text, c)
			p.pos++
			continue
		}

		p.pos++
		if p.pos == len(p.data) {
			return "", ErrInvalidJSON
		}

		escape := p.data[p.pos]
		p.pos++

		switch escape {
		case '"', '\\', '/':
			text = append(text, escape)
		case 'b':
			text = append(text, '\b')
		case 'f':
			text = append(text, '\f')
		case 'n':
			text = append(text, '\n')
		case 'r':
			text = append(text, '\r')
		case 't':
			text = append(text, '\t')
		case 'u':
			r, err := p.escapedRune()
			if err != nil {
				return "", err
			}
			text = utf8.AppendRune(text, r)
		default:
			return "", ErrInvalidJSON
		}
	}

	return "", ErrInvalidJSON
}

// escapedRune reads the hex digits of a \u escape, joining surrogate pairs.
// An unpaired surrogate is invalid JSON.
func (p *jsonParser) escapedRune() (rune, error) {
	first, ok := p.hex4()
	if !ok {
		return 0, ErrInvalidJSON
	}

	switch {
	case first >= 0xDC00 && first <= 0xDFFF:
		return 0, ErrInvalidJSON
	case first < 0xD800 || first > 0xDBFF:
		return first, nil
	}

	if p.pos+2 > len(p.data) || p.data[p.pos] != '\\' || p.data[p.pos+1] != 'u' {
		return 0, ErrInvalidJSON
	}
	p.pos += 2

	second, ok := p.hex4()
	if !ok || second < 0xDC00 || second > 0xDFFF {
		return 0, ErrInvalidJSON
	}

	return utf16.DecodeRune(first, second), nil
}

func (p *jsonParser) hex4() (rune, bool) {
	if p.pos+4 > len(p.data) {
		return 0, false
	}

	value, err := strconv.ParseUint(string(p.data[p.pos:p.pos+4]), 16, 32)
	if err != nil {
		return 0, false
	}
	p.pos += 4

	return rune(value), true
}

func (p *jsonParser) number() (*Value, error) {
	start := p.pos

	p.consume('-')

	switch {
	case p.consume('0'):
	case p.pos < len(p.data) && isDigit(p.data[p.pos]) && p.data[p.pos] != '0':
		p.digits()
	default:
		return nil, ErrInvalidJSON
	}

	if p.consume('.') && !p.digits() {
		return nil, ErrInvalidJSON
	}

	if p.consume('e') || p.consume('E') {
		if !p.consume('+') {
			p.consume('-')
		}
		if !p.digits() {
			return nil, ErrInvalidJSON
		}
	}

	return &Value{Kind: KindNumber, Text: string(p.data[start:p.pos])}, nil
}

// digits consumes one or more digits and reports whether it found any.
func (p *jsonParser) digits() bool {
	start := p.pos
	for p.pos < len(p.data) && isDigit(p.data[p.pos]) {
		p.pos++
	}
	return p.pos > start
}

func (p *jsonParser) literal(word string, kind Kind) (*Value, error) {
	if !bytes.HasPrefix(p.data[p.pos:], []byte(word)) {
		return nil, ErrInvalidJSON
	}
	p.pos += len(word)

	return &Value{Kind: kind, Text: word}, nil
}

func (p *jsonParser) consume(c byte) bool {
	if p.pos < len(p.data) && p.data[p.pos] == c {
		p.pos++
		return true
	}
	return false
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}
