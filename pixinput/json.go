package pixinput

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// jsonKind identifies the type of a parsed JSON value.
type jsonKind int

const (
	kindNull jsonKind = iota
	kindBool
	kindNumber
	kindString
	kindArray
	kindObject
)

// jsonValue is a parsed JSON value. Numbers keep their raw token.
type jsonValue struct {
	kind    jsonKind
	text    string
	members map[string]*jsonValue
	names   []string
}

var (
	errInvalidJSON  = errors.New("invalid JSON")
	errTrailingData = errors.New("trailing data")
)

var pointerEscaper = strings.NewReplacer("~", "~0", "/", "~1")

// pointer appends an escaped member name to a JSON Pointer.
func pointer(parent, name string) string {
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

// parseJSON parses one JSON value. It expects valid UTF-8.
func parseJSON(data []byte) (*jsonValue, []string, error) {
	p := &jsonParser{data: data}

	p.skipSpace()
	if p.pos == len(p.data) {
		return nil, nil, errInvalidJSON
	}

	value, err := p.value()
	if err != nil {
		return nil, nil, err
	}

	p.skipSpace()
	if p.pos != len(p.data) {
		return nil, nil, errTrailingData
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

func (p *jsonParser) value() (*jsonValue, error) {
	p.skipSpace()
	if p.pos == len(p.data) {
		return nil, errInvalidJSON
	}

	switch c := p.data[p.pos]; {
	case c == '{':
		return p.object()
	case c == '[':
		return p.array()
	case c == '"':
		text, err := p.string()
		return &jsonValue{kind: kindString, text: text}, err
	case c == 't':
		return p.literal("true", kindBool)
	case c == 'f':
		return p.literal("false", kindBool)
	case c == 'n':
		return p.literal("null", kindNull)
	case c == '-' || isDigit(c):
		return p.number()
	default:
		return nil, errInvalidJSON
	}
}

func (p *jsonParser) object() (*jsonValue, error) {
	object := &jsonValue{kind: kindObject, members: map[string]*jsonValue{}}
	p.pos++

	p.skipSpace()
	if p.consume('}') {
		return object, nil
	}

	for {
		p.skipSpace()
		if p.pos == len(p.data) || p.data[p.pos] != '"' {
			return nil, errInvalidJSON
		}

		name, err := p.string()
		if err != nil {
			return nil, err
		}

		p.skipSpace()
		if !p.consume(':') {
			return nil, errInvalidJSON
		}

		member, err := p.child(name)
		if err != nil {
			return nil, err
		}

		if _, exists := object.members[name]; exists {
			p.duplicates = append(p.duplicates, p.pointer(name))
		} else {
			object.members[name] = member
			object.names = append(object.names, name)
		}

		p.skipSpace()
		if p.consume('}') {
			return object, nil
		}

		if !p.consume(',') {
			return nil, errInvalidJSON
		}
	}
}

func (p *jsonParser) array() (*jsonValue, error) {
	p.pos++

	p.skipSpace()
	if p.consume(']') {
		return &jsonValue{kind: kindArray}, nil
	}

	for index := 0; ; index++ {
		if _, err := p.child(strconv.Itoa(index)); err != nil {
			return nil, err
		}

		p.skipSpace()
		if p.consume(']') {
			return &jsonValue{kind: kindArray}, nil
		}

		if !p.consume(',') {
			return nil, errInvalidJSON
		}
	}
}

// child parses the value of a member or array element. The path of the
// value is kept as segments and built only when a duplicate needs it, so
// deep nesting stays linear.
func (p *jsonParser) child(segment string) (*jsonValue, error) {
	p.segments = append(p.segments, segment)
	value, err := p.value()
	p.segments = p.segments[:len(p.segments)-1]

	return value, err
}

// pointer returns the JSON Pointer of member name in the current object.
func (p *jsonParser) pointer(name string) string {
	path := ""
	for _, segment := range p.segments {
		path = pointer(path, segment)
	}

	return pointer(path, name)
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
			return "", errInvalidJSON
		case c != '\\':
			text = append(text, c)
			p.pos++
			continue
		}

		p.pos++
		if p.pos == len(p.data) {
			return "", errInvalidJSON
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
			return "", errInvalidJSON
		}
	}

	return "", errInvalidJSON
}

// escapedRune reads the hex digits of a \u escape, joining surrogate pairs.
// An unpaired surrogate is invalid JSON.
func (p *jsonParser) escapedRune() (rune, error) {
	first, ok := p.hex4()
	if !ok {
		return 0, errInvalidJSON
	}

	switch {
	case first >= 0xDC00 && first <= 0xDFFF:
		return 0, errInvalidJSON
	case first < 0xD800 || first > 0xDBFF:
		return first, nil
	}

	if p.pos+2 > len(p.data) || p.data[p.pos] != '\\' || p.data[p.pos+1] != 'u' {
		return 0, errInvalidJSON
	}
	p.pos += 2

	second, ok := p.hex4()
	if !ok || second < 0xDC00 || second > 0xDFFF {
		return 0, errInvalidJSON
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

func (p *jsonParser) number() (*jsonValue, error) {
	start := p.pos

	p.consume('-')

	switch {
	case p.consume('0'):
	case p.pos < len(p.data) && isDigit(p.data[p.pos]) && p.data[p.pos] != '0':
		p.digits()
	default:
		return nil, errInvalidJSON
	}

	if p.consume('.') && !p.digits() {
		return nil, errInvalidJSON
	}

	if p.consume('e') || p.consume('E') {
		if !p.consume('+') {
			p.consume('-')
		}
		if !p.digits() {
			return nil, errInvalidJSON
		}
	}

	return &jsonValue{kind: kindNumber, text: string(p.data[start:p.pos])}, nil
}

// digits consumes one or more digits and reports whether it found any.
func (p *jsonParser) digits() bool {
	start := p.pos
	for p.pos < len(p.data) && isDigit(p.data[p.pos]) {
		p.pos++
	}
	return p.pos > start
}

func (p *jsonParser) literal(word string, kind jsonKind) (*jsonValue, error) {
	if !bytes.HasPrefix(p.data[p.pos:], []byte(word)) {
		return nil, errInvalidJSON
	}
	p.pos += len(word)

	return &jsonValue{kind: kind, text: word}, nil
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
