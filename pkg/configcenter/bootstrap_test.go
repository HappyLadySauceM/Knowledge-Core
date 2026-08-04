package configcenter

import (
	"testing"
	"time"
)

func TestParseEndpointsIsStrictAndCanonical(t *testing.T) {
	t.Parallel()
	endpoints, err := parseEndpoints("https://nacos:8848,https://[::1]:8848")
	if err != nil {
		t.Fatalf("parse endpoints: %v", err)
	}
	if len(endpoints) != 2 || endpoints[0].Address() != "nacos:8848" || endpoints[1].Address() != "[::1]:8848" {
		t.Fatalf("unexpected endpoints: %#v", endpoints)
	}
	for _, value := range []string{
		"",
		"nacos:8848",
		"http://nacos:8848",
		"http://nacos",
		"http://user@nacos:8848",
		"http://nacos:8848/path",
		"http://nacos:8848,http://nacos:8848",
	} {
		if _, err := parseEndpoints(value); err == nil {
			t.Fatalf("invalid endpoints accepted: %q", value)
		}
	}
}

func TestParseEnvironmentDurationUsesPortableUnits(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]time.Duration{
		"1500ms": 1500 * time.Millisecond,
		"5s":     5 * time.Second,
		"2m":     2 * time.Minute,
	} {
		got, err := parseEnvironmentDuration(raw)
		if err != nil {
			t.Fatalf("parse duration %q: %v", raw, err)
		}
		if got != want {
			t.Fatalf("parse duration %q = %s, want %s", raw, got, want)
		}
	}

	for _, raw := range []string{"", "5000", "1.5s", "0s", "-1s", " 5s", "5h"} {
		if _, err := parseEnvironmentDuration(raw); err == nil {
			t.Fatalf("invalid duration accepted: %q", raw)
		}
	}
}
