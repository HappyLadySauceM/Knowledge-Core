package configcenter

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	corelog "github.com/HappyLadySauce/Knowledge-Core/pkg/log"
	"gopkg.in/yaml.v3"
)

const (
	DynamicAPIVersion     = "knowledge-core.io/v1alpha1"
	DynamicKind           = "DynamicConfig"
	ApplicationAPIVersion = "knowledge-core.io/v1beta1"
	ApplicationKind       = "ApplicationConfig"
)

type DynamicDocument struct {
	APIVersion string         `yaml:"api_version" json:"api_version"`
	Kind       string         `yaml:"kind" json:"kind"`
	Service    string         `yaml:"service,omitempty" json:"service,omitempty"`
	Revision   uint64         `yaml:"revision" json:"revision"`
	Log        DynamicLog     `yaml:"log,omitempty" json:"log,omitempty"`
	Config     map[string]any `yaml:"config,omitempty" json:"config,omitempty"`
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
	if d.Revision == 0 {
		return errors.New("validate dynamic configuration: revision must be positive")
	}
	switch {
	case d.APIVersion == DynamicAPIVersion && d.Kind == DynamicKind:
		if d.Service != "" || len(d.Config) != 0 {
			return errors.New("validate dynamic configuration: v1alpha1 documents cannot contain service or config")
		}
		if strings.TrimSpace(d.Log.Level) != d.Log.Level {
			return errors.New("validate dynamic configuration: log.level must not contain surrounding whitespace")
		}
		if _, err := corelog.ParseLevel(d.Log.Level); err != nil {
			return fmt.Errorf("validate dynamic configuration: %w", err)
		}
	case d.APIVersion == ApplicationAPIVersion && d.Kind == ApplicationKind:
		if strings.TrimSpace(d.Service) == "" || strings.TrimSpace(d.Service) != d.Service {
			return errors.New("validate dynamic configuration: service must be non-empty and trimmed")
		}
		if len(d.Config) == 0 {
			return errors.New("validate dynamic configuration: config must be a non-empty mapping")
		}
		if d.Log.Level != "" {
			return errors.New("validate dynamic configuration: v1beta1 log settings belong under config.log")
		}
	default:
		return fmt.Errorf("validate dynamic configuration: unsupported api_version/kind %q/%q", d.APIVersion, d.Kind)
	}
	return nil
}

func (d DynamicDocument) Legacy() bool { return d.APIVersion == DynamicAPIVersion }

func (d DynamicDocument) Equal(other DynamicDocument) bool { return reflect.DeepEqual(d, other) }

func (d DynamicDocument) ValidateApplication(service string, forbiddenPaths ...string) error {
	if d.Legacy() {
		return nil
	}
	if d.Service != service {
		return fmt.Errorf("validate dynamic configuration: service must be %q", service)
	}
	for _, path := range forbiddenPaths {
		if mappingHasPath(d.Config, strings.Split(path, ".")) {
			return fmt.Errorf("validate dynamic configuration: sensitive field %q is not allowed", path)
		}
	}
	return nil
}

func mappingHasPath(mapping map[string]any, path []string) bool {
	if len(path) == 0 {
		return false
	}
	value, exists := mapping[path[0]]
	if !exists {
		return false
	}
	if len(path) == 1 {
		return true
	}
	nested, ok := value.(map[string]any)
	return ok && mappingHasPath(nested, path[1:])
}
