package config

import (
	"context"
	"errors"
	"fmt"
)

// Load merges sources from lowest to highest priority.
func Load(ctx context.Context, sources ...Source) (Snapshot, error) {
	merged := make(Snapshot)
	for _, source := range sources {
		if source == nil {
			return nil, errors.New("load config: nil source")
		}
		snapshot, err := source.Load(ctx)
		if err != nil {
			return nil, fmt.Errorf("load config source %q: %w", source.Name(), err)
		}
		for key, value := range snapshot {
			merged[key] = append([]byte(nil), value...)
		}
	}
	return merged, nil
}
