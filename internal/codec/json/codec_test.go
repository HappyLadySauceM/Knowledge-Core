package json_test

import (
	stdjson "encoding/json"
	"strings"
	"testing"

	internaljson "github.com/HappyLadySauce/Knowledge-Core/internal/codec/json"
)

func TestDecodeStrictUsesNumber(t *testing.T) {
	codec := internaljson.New()
	var payload struct {
		Value any `json:"value"`
	}

	err := codec.Decode(strings.NewReader(`{"value":9007199254740993}`), &payload, internaljson.DecodeOptions{
		DisallowUnknownFields: true,
		UseNumber:             true,
	})
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	value, ok := payload.Value.(stdjson.Number)
	if !ok {
		t.Fatalf("value type = %T, want json.Number", payload.Value)
	}
	if value.String() != "9007199254740993" {
		t.Fatalf("value = %q", value.String())
	}
}

func TestDecodeStrictRejectsUnknownAndTrailingValues(t *testing.T) {
	codec := internaljson.New()
	var payload struct {
		Name string `json:"name"`
	}

	if err := codec.Decode(strings.NewReader(`{"name":"gateway","extra":true}`), &payload, internaljson.DecodeOptions{DisallowUnknownFields: true}); err == nil {
		t.Fatal("Decode() accepted an unknown field")
	}
	if err := codec.Decode(strings.NewReader(`{"name":"gateway"} {"name":"identity"}`), &payload, internaljson.DecodeOptions{}); err == nil {
		t.Fatal("Decode() accepted multiple JSON values")
	}
}
