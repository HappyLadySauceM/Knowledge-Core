package config

import "context"

type Snapshot map[string][]byte

type ChangeHandler func(context.Context, Snapshot) error

type Source interface {
	Name() string
	Load(ctx context.Context) (Snapshot, error)
	Close() error
}

type WatchSource interface {
	Source
	Watch(ctx context.Context, onChange ChangeHandler) error
}

func Clone(snapshot Snapshot) Snapshot {
	clone := make(Snapshot, len(snapshot))
	for key, value := range snapshot {
		clone[key] = append([]byte(nil), value...)
	}
	return clone
}
