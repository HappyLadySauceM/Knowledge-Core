package json

import (
	"errors"
	"fmt"
	"io"

	"github.com/bytedance/sonic"
)

// DecodeOptions controls validation for streamed JSON input.
type DecodeOptions struct {
	DisallowUnknownFields bool
	UseNumber             bool
}

// Codec is the JSON boundary used by transports, configuration, and events.
type Codec interface {
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
	Decode(r io.Reader, v any, opts DecodeOptions) error
}

type Sonic struct {
	api sonic.API
}

func New() *Sonic {
	return &Sonic{api: sonic.ConfigDefault}
}

func (c *Sonic) Marshal(v any) ([]byte, error) {
	data, err := c.api.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal JSON: %w", err)
	}
	return data, nil
}

func (c *Sonic) Unmarshal(data []byte, v any) error {
	if err := c.api.Unmarshal(data, v); err != nil {
		return fmt.Errorf("unmarshal JSON: %w", err)
	}
	return nil
}

func (c *Sonic) Decode(r io.Reader, v any, opts DecodeOptions) error {
	decoder := c.api.NewDecoder(r)
	if opts.DisallowUnknownFields {
		decoder.DisallowUnknownFields()
	}
	if opts.UseNumber {
		decoder.UseNumber()
	}
	if err := decoder.Decode(v); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}

	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("decode JSON: multiple values are not allowed")
		}
		return fmt.Errorf("decode trailing JSON: %w", err)
	}
	return nil
}

var _ Codec = (*Sonic)(nil)
