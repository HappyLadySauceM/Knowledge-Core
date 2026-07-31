package json_test

import (
	stdjson "encoding/json"
	"errors"
	"testing"

	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
)

func TestUnmarshalRejectsUnknownAndMultipleValues(t *testing.T) {
	type request struct {
		Name string `json:"name"`
	}
	for _, input := range []string{
		`{"name":"member","admin":true}`,
		`{"name":"member"} {"name":"other"}`,
	} {
		var decoded request
		if err := jsoncodec.Unmarshal([]byte(input), &decoded); err == nil {
			t.Fatalf("Unmarshal(%q) accepted invalid input", input)
		}
	}
}

func TestUnmarshalPreservesNumbers(t *testing.T) {
	var decoded map[string]any
	if err := jsoncodec.Unmarshal([]byte(`{"id":9007199254740993}`), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	number, ok := decoded["id"].(stdjson.Number)
	if !ok || number.String() != "9007199254740993" {
		t.Fatalf("decoded number = %#v", decoded["id"])
	}
}

func TestUnmarshalReportsMultipleValues(t *testing.T) {
	var decoded map[string]any
	err := jsoncodec.Unmarshal([]byte(`{} {}`), &decoded)
	if !errors.Is(err, jsoncodec.ErrMultipleValues) {
		t.Fatalf("Unmarshal() error = %v", err)
	}
}
