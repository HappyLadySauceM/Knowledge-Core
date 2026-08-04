package configcenter

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	corelog "github.com/HappyLadySauce/Knowledge-Core/pkg/log"
	"gopkg.in/yaml.v3"
)

const (
	DynamicAPIVersion = "knowledge-core.io/v1alpha1"
	DynamicKind       = "DynamicConfig"
)

type DynamicDocument struct {
	APIVersion string     `yaml:"api_version" json:"api_version"`
	Kind       string     `yaml:"kind" json:"kind"`
	Revision   uint64     `yaml:"revision" json:"revision"`
	Log        DynamicLog `yaml:"log" json:"log"`
}

type DynamicLog struct {
	Level string `yaml:"level" json:"level"`
}

func DecodeDynamicDocument(contents []byte) (DynamicDocument, error) {
	if len(contents) == 0 || len(contents) > maximumContent {
		return DynamicDocument{}, errors.New("decode dynamic configuration: document size is invalid")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	decoder.KnownFields(true)
	var document DynamicDocument
	if err := decoder.Decode(&document); err != nil {
		return DynamicDocument{}, fmt.Errorf("decode dynamic configuration: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return DynamicDocument{}, errors.New("decode dynamic configuration: multiple YAML documents are not allowed")
		}
		return DynamicDocument{}, fmt.Errorf("decode dynamic configuration trailing content: %w", err)
	}
	if err := document.Validate(); err != nil {
		return DynamicDocument{}, err
	}
	return document, nil
}

func (d DynamicDocument) Validate() error {
	if d.APIVersion != DynamicAPIVersion {
		return fmt.Errorf("validate dynamic configuration: api_version must be %q", DynamicAPIVersion)
	}
	if d.Kind != DynamicKind {
		return fmt.Errorf("validate dynamic configuration: kind must be %q", DynamicKind)
	}
	if d.Revision == 0 {
		return errors.New("validate dynamic configuration: revision must be positive")
	}
	if strings.TrimSpace(d.Log.Level) != d.Log.Level {
		return errors.New("validate dynamic configuration: log.level must not contain surrounding whitespace")
	}
	if _, err := corelog.ParseLevel(d.Log.Level); err != nil {
		return fmt.Errorf("validate dynamic configuration: %w", err)
	}
	return nil
}
