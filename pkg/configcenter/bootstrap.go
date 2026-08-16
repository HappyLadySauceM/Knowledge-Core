package configcenter

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const envPrefix = "KNOWLEDGE_CORE_NACOS_"

type Endpoint struct {
	Scheme string
	Host   string
	Port   uint64
}

func (e Endpoint) Address() string {
	return net.JoinHostPort(e.Host, strconv.FormatUint(e.Port, 10))
}

type Bootstrap struct {
	Enabled      bool
	TLSEnabled   bool
	Service      string
	Endpoints    []Endpoint
	Binding      Binding
	Username     string
	Password     string
	CAFile       string
	KeyID        string
	Key          []byte
	Timeout      time.Duration
	PollInterval time.Duration
	RuntimeDir   string
}

func BootstrapFromEnvironment(service string) (Bootstrap, error) {
	enabled, err := environmentBoolean(envPrefix+"ENABLED", false)
	if err != nil {
		return Bootstrap{}, err
	}
	service = strings.TrimSpace(service)
	if service == "" {
		return Bootstrap{}, errors.New("configure Nacos: service name is required")
	}
	bootstrap := Bootstrap{Enabled: enabled, Service: service, TLSEnabled: true}
	if !enabled {
		return bootstrap, nil
	}
	bootstrap.TLSEnabled, err = environmentBoolean(envPrefix+"TLS_ENABLED", true)
	if err != nil {
		return Bootstrap{}, err
	}
	endpoints, err := parseEndpoints(os.Getenv(envPrefix+"SERVERS"), bootstrap.TLSEnabled)
	if err != nil {
		return Bootstrap{}, err
	}
	bootstrap.Endpoints = endpoints
	bootstrap.Binding = Binding{
		Namespace: strings.TrimSpace(os.Getenv(envPrefix + "NAMESPACE")),
		Group:     environmentValue(envPrefix+"GROUP", "KNOWLEDGE_CORE"),
		DataID:    environmentValue(envPrefix+"DATA_ID", service+".dynamic.yaml"),
	}
	if err := bootstrap.Binding.Validate(); err != nil {
		return Bootstrap{}, fmt.Errorf("configure Nacos: %w", err)
	}
	bootstrap.Username = strings.TrimSpace(os.Getenv(envPrefix + "USERNAME"))
	bootstrap.Password = os.Getenv(envPrefix + "PASSWORD")
	if bootstrap.Username == "" || bootstrap.Password == "" {
		return Bootstrap{}, errors.New("configure Nacos: username and password are required when remote configuration is enabled")
	}
	bootstrap.CAFile = strings.TrimSpace(os.Getenv(envPrefix + "CA_FILE"))
	if bootstrap.TLSEnabled && !filepath.IsAbs(bootstrap.CAFile) {
		return Bootstrap{}, errors.New("configure Nacos: CA file must be an absolute path")
	}
	if !bootstrap.TLSEnabled && bootstrap.CAFile != "" {
		return Bootstrap{}, errors.New("configure Nacos: CA file must be empty when TLS is disabled")
	}
	bootstrap.KeyID = strings.TrimSpace(os.Getenv(envPrefix + "KEY_ID"))
	if bootstrap.KeyID == "" {
		return Bootstrap{}, errors.New("configure Nacos: key identifier is required")
	}
	bootstrap.Key, err = ParseKey(os.Getenv(envPrefix + "KEK"))
	if err != nil {
		return Bootstrap{}, fmt.Errorf("configure Nacos: %w", err)
	}
	bootstrap.Timeout, err = environmentDuration(envPrefix+"TIMEOUT", 5*time.Second, time.Second, 30*time.Second)
	if err != nil {
		return Bootstrap{}, err
	}
	bootstrap.PollInterval, err = environmentDuration(envPrefix+"POLL_INTERVAL", 30*time.Second, 5*time.Second, 5*time.Minute)
	if err != nil {
		return Bootstrap{}, err
	}
	bootstrap.RuntimeDir = environmentValue(envPrefix+"RUNTIME_DIR", filepath.Join(os.TempDir(), "knowledge-core", "nacos", service))
	if !filepath.IsAbs(bootstrap.RuntimeDir) {
		return Bootstrap{}, errors.New("configure Nacos: runtime directory must be absolute")
	}
	return bootstrap, nil
}

func parseEndpoints(raw string, tlsEnabled bool) ([]Endpoint, error) {
	parts := strings.Split(raw, ",")
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("configure Nacos: at least one server is required")
	}
	endpoints := make([]Endpoint, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		parsed, err := url.Parse(part)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return nil, fmt.Errorf("configure Nacos: server %q must be an absolute HTTP URL", part)
		}
		expectedScheme := "http"
		if tlsEnabled {
			expectedScheme = "https"
		}
		if parsed.Scheme != expectedScheme {
			return nil, fmt.Errorf("configure Nacos: server %q must use %s", part, expectedScheme)
		}
		if parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, fmt.Errorf("configure Nacos: server %q must not contain credentials, path, query, or fragment", part)
		}
		host := parsed.Hostname()
		portText := parsed.Port()
		if host == "" || portText == "" {
			return nil, fmt.Errorf("configure Nacos: server %q must include host and port", part)
		}
		port, err := strconv.ParseUint(portText, 10, 16)
		if err != nil || port == 0 {
			return nil, fmt.Errorf("configure Nacos: server %q contains an invalid port", part)
		}
		endpoint := Endpoint{Scheme: parsed.Scheme, Host: host, Port: port}
		key := endpoint.Scheme + "://" + endpoint.Address()
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("configure Nacos: duplicate server %q", part)
		}
		seen[key] = struct{}{}
		endpoints = append(endpoints, endpoint)
	}
	return endpoints, nil
}

func environmentBoolean(name string, fallback bool) (bool, error) {
	raw, exists := os.LookupEnv(name)
	if !exists {
		return fallback, nil
	}
	switch raw {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("configure Nacos: %s must be true or false", name)
	}
}

func environmentDuration(name string, fallback, minimum, maximum time.Duration) (time.Duration, error) {
	raw := environmentValue(name, fallback.String())
	value, err := parseEnvironmentDuration(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("configure Nacos: %s must be an integer duration using ms, s, or m between %s and %s", name, minimum, maximum)
	}
	return value, nil
}

func parseEnvironmentDuration(raw string) (time.Duration, error) {
	var unit time.Duration
	var number string
	switch {
	case strings.HasSuffix(raw, "ms"):
		unit, number = time.Millisecond, strings.TrimSuffix(raw, "ms")
	case strings.HasSuffix(raw, "s"):
		unit, number = time.Second, strings.TrimSuffix(raw, "s")
	case strings.HasSuffix(raw, "m"):
		unit, number = time.Minute, strings.TrimSuffix(raw, "m")
	default:
		return 0, errors.New("duration unit must be ms, s, or m")
	}
	value, err := strconv.ParseUint(number, 10, 64)
	if err != nil || value == 0 || value > uint64((1<<63-1)/unit) {
		return 0, errors.New("duration value must be a positive integer")
	}
	return time.Duration(value) * unit, nil
}

func environmentValue(name, fallback string) string {
	if raw, exists := os.LookupEnv(name); exists {
		return strings.TrimSpace(raw)
	}
	return fallback
}
