package env

import (
	"context"
	"os"

	"github.com/HappyLadySauce/Knowledge-Core/internal/config"
)

type LookupFunc func(string) (string, bool)

type Source struct {
	mapping map[string]string
	lookup  LookupFunc
}

// New maps environment variable names to canonical configuration keys.
func New(mapping map[string]string) *Source {
	return NewWithLookup(mapping, os.LookupEnv)
}

func NewWithLookup(mapping map[string]string, lookup LookupFunc) *Source {
	copied := make(map[string]string, len(mapping))
	for environmentName, configKey := range mapping {
		copied[environmentName] = configKey
	}
	return &Source{mapping: copied, lookup: lookup}
}

func (s *Source) Name() string { return "environment" }

func (s *Source) Load(context.Context) (config.Snapshot, error) {
	snapshot := make(config.Snapshot)
	for environmentName, configKey := range s.mapping {
		if value, exists := s.lookup(environmentName); exists {
			snapshot[configKey] = []byte(value)
		}
	}
	return snapshot, nil
}

func (s *Source) Close() error { return nil }

var _ config.Source = (*Source)(nil)
