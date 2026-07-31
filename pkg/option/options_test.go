package option

import (
	"reflect"
	"strings"
	"testing"
)

func TestDefaultOptionsValidate(t *testing.T) {
	t.Parallel()
	options := []struct {
		name     string
		validate func() error
	}{
		{"app", NewAppOptions().Validate},
		{"log", NewLogOptions().Validate},
		{"trace", NewTraceOptions().Validate},
		{"kitex", NewKitexServerOptions().Validate},
		{"hertz", NewHertzServerOptions().Validate},
		{"postgres", NewPostgreSQLOptions().Validate},
		{"redis", NewRedisOptions().Validate},
		{"etcd", NewEtcdOptions().Validate},
		{"nats", NewNATSOptions().Validate},
		{"tls", NewTLSOptions().Validate},
	}
	for _, test := range options {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.validate(); err != nil {
				t.Fatalf("default Validate() error = %v", err)
			}
		})
	}
}

func TestValidateAggregatesIndependentErrors(t *testing.T) {
	t.Parallel()
	opts := *NewRedisOptions()
	opts.Address = ""
	opts.DB = -1
	opts.PoolSize = 0
	opts.DialTimeout = 0
	err := opts.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil")
	}
	for _, expected := range []string{"redis.address", "redis.db", "redis.pool_size", "redis.dial_timeout"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("Validate() error = %q, want %q", err, expected)
		}
	}
}

func TestAllOptionFieldsHaveSerializationTags(t *testing.T) {
	t.Parallel()
	types := []reflect.Type{
		reflect.TypeOf(AppOptions{}), reflect.TypeOf(LogOptions{}), reflect.TypeOf(TraceOptions{}),
		reflect.TypeOf(KitexServerOptions{}), reflect.TypeOf(HertzServerOptions{}),
		reflect.TypeOf(PostgreSQLOptions{}), reflect.TypeOf(RedisOptions{}),
		reflect.TypeOf(EtcdOptions{}), reflect.TypeOf(NATSOptions{}), reflect.TypeOf(TLSOptions{}),
	}
	for _, optionType := range types {
		for index := 0; index < optionType.NumField(); index++ {
			field := optionType.Field(index)
			for _, tag := range []string{"mapstructure", "yaml", "json"} {
				if field.Tag.Get(tag) == "" {
					t.Errorf("%s.%s is missing %s tag", optionType.Name(), field.Name, tag)
				}
			}
		}
	}
}

func TestTLSBuildersFailClosed(t *testing.T) {
	t.Parallel()
	if config, err := (TLSOptions{}).ClientTLSConfig(); err != nil || config != nil {
		t.Fatalf("disabled ClientTLSConfig() = (%v, %v), want (nil, nil)", config, err)
	}
	if config, err := (TLSOptions{}).ServerTLSConfig(); err != nil || config != nil {
		t.Fatalf("disabled ServerTLSConfig() = (%v, %v), want (nil, nil)", config, err)
	}
	if _, err := (TLSOptions{Enabled: true}).ServerTLSConfig(); err == nil || !strings.Contains(err.Error(), "requires") {
		t.Fatalf("ServerTLSConfig() error = %v, want required certificate error", err)
	}
	if _, err := (TLSOptions{Enabled: true, CAFile: "does-not-exist.pem"}).ClientTLSConfig(); err == nil || !strings.Contains(err.Error(), "read TLS CA") {
		t.Fatalf("ClientTLSConfig() error = %v, want CA read error", err)
	}
}
