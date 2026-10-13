package trainingdata

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// readBufferSize is the size of the read buffer. Lines longer than the
// buffer are assembled in pieces, up to MaxRecordSize.
const readBufferSize = 64 * 1024

// Scan reads a training dataset one line at a time, validates and
// classifies every record, and calls visit for each record in file order.
// Memory stays bounded by MaxRecordSize, whatever the dataset size. It
// returns the dataset summary, including the SHA-256 of the exact bytes
// read. A reader failure, a feature schema failure, or an error from visit
// stops the scan and is returned.
func Scan(r io.Reader, schema FeatureSchema, visit func(Record) error) (Summary, error) {
	hash := sha256.New()
	counter := &byteCounter{}
	reader := bufio.NewReaderSize(io.TeeReader(r, io.MultiWriter(hash, counter)), readBufferSize)

	summary := Summary{FeatureSchema: schema.Name}
	lines := &lineReader{reader: reader}

	for number := 1; ; number++ {
		data, tooLarge, err := lines.next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return summary, fmt.Errorf("read training dataset: %w", err)
		}

		record, err := decodeRecord(number, data, tooLarge, schema)
		if err != nil {
			return summary, err
		}

		summary.add(record)

		if err := visit(record); err != nil {
			return summary, err
		}
	}

	summary.DatasetSHA256 = hex.EncodeToString(hash.Sum(nil))
	summary.DatasetBytes = counter.bytes

	return summary, nil
}

// lineReader splits a stream into LF-terminated lines and keeps at most
// MaxRecordSize bytes of any line.
type lineReader struct {
	reader *bufio.Reader
	line   []byte
}

// next returns the next line without its LF, and whether the line exceeded
// MaxRecordSize, in which case its bytes are discarded. It returns io.EOF
// when no line remains.
func (l *lineReader) next() ([]byte, bool, error) {
	l.line = l.line[:0]
	tooLarge := false
	started := false

	for {
		chunk, err := l.reader.ReadSlice('\n')
		if len(chunk) > 0 {
			started = true
		}

		content := chunk
		if err == nil {
			content = chunk[:len(chunk)-1]
		}

		if !tooLarge {
			if len(l.line)+len(content) > MaxRecordSize {
				tooLarge = true
				l.line = l.line[:0]
			} else {
				l.line = append(l.line, content...)
			}
		}

		switch {
		case err == nil:
			return l.line, tooLarge, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && started:
			return l.line, tooLarge, nil
		default:
			return nil, false, err
		}
	}
}

type byteCounter struct {
	bytes int64
}

func (c *byteCounter) Write(p []byte) (int, error) {
	c.bytes += int64(len(p))
	return len(p), nil
}
