// Package json is the service-wide Sonic JSON contract.
package json

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/bytedance/sonic"
)

var (
	ErrMultipleValues = errors.New("decode JSON: multiple values are not allowed")
	strictAPI         = sonic.Config{
		EscapeHTML:            true,
		SortMapKeys:           true,
		CopyString:            true,
		ValidateString:        true,
		UseUnicodeErrors:      true,
		UseNumber:             true,
		DisallowUnknownFields: true,
	}.Froze()
)

func Marshal(value any) ([]byte, error) {
	return strictAPI.Marshal(value)
}

func MarshalString(value any) (string, error) {
	return strictAPI.MarshalToString(value)
}

func MarshalIndent(value any, prefix, indent string) ([]byte, error) {
	return strictAPI.MarshalIndent(value, prefix, indent)
}

// Unmarshal is strict: unknown struct fields, malformed Unicode and additional
// top-level JSON values are rejected, and interface numbers remain json.Number.
func Unmarshal(data []byte, value any) error {
	return Decode(bytes.NewReader(data), value)
}

func UnmarshalString(data string, value any) error {
	return Decode(strings.NewReader(data), value)
}

func Decode(reader io.Reader, value any) error {
	if reader == nil {
		return errors.New("decode JSON: reader is nil")
	}
	decoder := strictAPI.NewDecoder(reader)
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return ErrMultipleValues
		}
		return fmt.Errorf("decode JSON trailing data: %w", err)
	}
	return nil
}

func NewEncoder(writer io.Writer) sonic.Encoder {
	return strictAPI.NewEncoder(writer)
}

// NewDecoder creates a configured stream decoder. Use Decode when the input
// must contain exactly one top-level value (for example, an HTTP request body).
func NewDecoder(reader io.Reader) sonic.Decoder {
	return strictAPI.NewDecoder(reader)
}

func Valid(data []byte) bool {
	return strictAPI.Valid(data)
}
