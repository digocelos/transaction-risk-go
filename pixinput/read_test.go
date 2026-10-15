package pixinput

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// endlessReader supplies an unbounded document and counts the bytes read.
type endlessReader struct {
	read int
}

func (r *endlessReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	if r.read == 0 && len(p) > 0 {
		p[0] = '{'
	}

	r.read += len(p)
	return len(p), nil
}

type failingReader struct{}

var errDisk = errors.New("disk failure")

func (failingReader) Read([]byte) (int, error) {
	return 0, errDisk
}

func TestReadDecodesAValidDocument(t *testing.T) {
	input, err := Read(bytes.NewReader(readExample(t, completeExample)), ContractV2)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	if input.PixPayment.AmountCents != 250000 {
		t.Errorf("AmountCents = %d", input.PixPayment.AmountCents)
	}
}

func TestReadStopsAfterTheSizeLimit(t *testing.T) {
	reader := &endlessReader{}

	_, err := Read(reader, ContractV2)

	want := []pathCode{{"", "document_too_large"}}
	if got := issuesOf(t, err); len(got) != 1 || got[0] != want[0] {
		t.Errorf("issues = %v, want %v", got, want)
	}

	if reader.read > MaxDocumentSize+1 {
		t.Errorf("read %d bytes, want at most %d", reader.read, MaxDocumentSize+1)
	}
}

func TestReadReturnsReaderFailures(t *testing.T) {
	_, err := Read(io.MultiReader(bytes.NewReader([]byte("{")), failingReader{}), ContractV2)

	var validationErr *ValidationError
	if errors.As(err, &validationErr) {
		t.Fatalf("error = %v, want a reader failure, not a validation error", err)
	}

	if !errors.Is(err, errDisk) {
		t.Errorf("error = %v, want it to wrap %v", err, errDisk)
	}
}
