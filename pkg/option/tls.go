package option

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// TLSOptions describes client-side TLS and optional mutual TLS credentials.
type TLSOptions struct {
	Enabled            bool   `mapstructure:"enabled" json:"enabled" yaml:"enabled"`
	CAFile             string `mapstructure:"ca_file" json:"ca_file" yaml:"ca_file"`
	CertFile           string `mapstructure:"cert_file" json:"cert_file" yaml:"cert_file"`
	KeyFile            string `mapstructure:"key_file" json:"key_file" yaml:"key_file"`
	ServerName         string `mapstructure:"server_name" json:"server_name" yaml:"server_name"`
	InsecureSkipVerify bool   `mapstructure:"insecure_skip_verify" json:"insecure_skip_verify" yaml:"insecure_skip_verify"`
}

func NewTLSOptions() *TLSOptions { return &TLSOptions{} }

func (o TLSOptions) Validate() error {
	configured := o.CAFile != "" || o.CertFile != "" || o.KeyFile != "" || o.ServerName != "" || o.InsecureSkipVerify
	var disabledErr error
	if !o.Enabled && configured {
		disabledErr = fmt.Errorf("tls settings require tls.enabled=true")
	}
	var pairErr error
	if (o.CertFile == "") != (o.KeyFile == "") {
		pairErr = fmt.Errorf("tls.cert_file and tls.key_file must be configured together")
	}
	return join(disabledErr, pairErr)
}

// ClientTLSConfig validates and loads trust roots and an optional mTLS identity.
// A nil config means TLS is intentionally disabled.
func (o TLSOptions) ClientTLSConfig() (*tls.Config, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	if !o.Enabled {
		return nil, nil
	}

	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if o.CAFile != "" {
		contents, readErr := os.ReadFile(o.CAFile)
		if readErr != nil {
			return nil, fmt.Errorf("read TLS CA file: %w", readErr)
		}
		if ok := roots.AppendCertsFromPEM(contents); !ok {
			return nil, fmt.Errorf("parse TLS CA file %q: no PEM certificates found", o.CAFile)
		}
	}

	config := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		RootCAs:            roots,
		ServerName:         o.ServerName,
		InsecureSkipVerify: o.InsecureSkipVerify, //nolint:gosec // explicit development escape hatch.
	}
	if o.CertFile != "" {
		certificate, loadErr := tls.LoadX509KeyPair(o.CertFile, o.KeyFile)
		if loadErr != nil {
			return nil, fmt.Errorf("load TLS client certificate: %w", loadErr)
		}
		config.Certificates = []tls.Certificate{certificate}
	}
	return config, nil
}

// ServerTLSConfig loads a server certificate. When CAFile is configured it
// enables mutual TLS and requires every client to present a trusted identity.
func (o TLSOptions) ServerTLSConfig() (*tls.Config, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	if !o.Enabled {
		return nil, nil
	}
	if o.CertFile == "" || o.KeyFile == "" {
		return nil, fmt.Errorf("server TLS requires tls.cert_file and tls.key_file")
	}
	if o.ServerName != "" {
		return nil, fmt.Errorf("tls.server_name is a client-only setting")
	}
	if o.InsecureSkipVerify {
		return nil, fmt.Errorf("tls.insecure_skip_verify is a client-only setting")
	}
	certificate, err := tls.LoadX509KeyPair(o.CertFile, o.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS server certificate: %w", err)
	}
	config := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{certificate},
	}
	if o.CAFile != "" {
		contents, readErr := os.ReadFile(o.CAFile)
		if readErr != nil {
			return nil, fmt.Errorf("read TLS client CA file: %w", readErr)
		}
		clientRoots := x509.NewCertPool()
		if ok := clientRoots.AppendCertsFromPEM(contents); !ok {
			return nil, fmt.Errorf("parse TLS client CA file %q: no PEM certificates found", o.CAFile)
		}
		config.ClientCAs = clientRoots
		config.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return config, nil
}
