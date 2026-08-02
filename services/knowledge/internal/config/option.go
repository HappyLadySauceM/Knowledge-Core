package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
)

var bucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

type AuthOptions struct {
	PublicKey string `mapstructure:"public_key" json:"public_key" yaml:"public_key"`
}

func NewAuthOptions() *AuthOptions { return &AuthOptions{} }

func (o AuthOptions) Validate() error {
	_, err := coreauth.NewVerifier(o.PublicKey)
	return err
}

type ObjectStorageOptions struct {
	Endpoint         string        `mapstructure:"endpoint" json:"endpoint" yaml:"endpoint"`
	Region           string        `mapstructure:"region" json:"region" yaml:"region"`
	Bucket           string        `mapstructure:"bucket" json:"bucket" yaml:"bucket"`
	AccessKey        string        `mapstructure:"access_key" json:"access_key" yaml:"access_key"`
	SecretKey        string        `mapstructure:"secret_key" json:"secret_key" yaml:"secret_key"`
	Secure           bool          `mapstructure:"secure" json:"secure" yaml:"secure"`
	AutoCreateBucket bool          `mapstructure:"auto_create_bucket" json:"auto_create_bucket" yaml:"auto_create_bucket"`
	UploadTTL        time.Duration `mapstructure:"upload_ttl" json:"upload_ttl" yaml:"upload_ttl"`
	DownloadTTL      time.Duration `mapstructure:"download_ttl" json:"download_ttl" yaml:"download_ttl"`
}

func NewObjectStorageOptions() *ObjectStorageOptions {
	return &ObjectStorageOptions{
		Endpoint: "127.0.0.1:9000", Region: "us-east-1", Bucket: "knowledge-core",
		UploadTTL: 15 * time.Minute, DownloadTTL: 5 * time.Minute,
	}
}

func (o ObjectStorageOptions) Validate() error {
	var joined error
	host, port, err := net.SplitHostPort(strings.TrimSpace(o.Endpoint))
	if err != nil || strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
		joined = errors.Join(joined, errors.New("object_storage.endpoint must be a host:port endpoint"))
	}
	if !bucketPattern.MatchString(o.Bucket) || strings.Contains(o.Bucket, "..") || net.ParseIP(o.Bucket) != nil {
		joined = errors.Join(joined, errors.New("object_storage.bucket must be a valid DNS-compatible S3 bucket name"))
	}
	if o.AccessKey == "" || o.SecretKey == "" {
		joined = errors.Join(joined, errors.New("object_storage.access_key and secret_key are required"))
	}
	if strings.TrimSpace(o.Region) == "" {
		joined = errors.Join(joined, errors.New("object_storage.region is required"))
	}
	if o.UploadTTL <= 0 || o.UploadTTL > 24*time.Hour {
		joined = errors.Join(joined, errors.New("object_storage.upload_ttl must be between 1ns and 24h"))
	}
	if o.DownloadTTL <= 0 || o.DownloadTTL > time.Hour {
		joined = errors.Join(joined, errors.New("object_storage.download_ttl must be between 1ns and 1h"))
	}
	return joined
}

type ScannerOptions struct {
	Address       string        `mapstructure:"address" json:"address" yaml:"address"`
	DialTimeout   time.Duration `mapstructure:"dial_timeout" json:"dial_timeout" yaml:"dial_timeout"`
	ScanTimeout   time.Duration `mapstructure:"scan_timeout" json:"scan_timeout" yaml:"scan_timeout"`
	MaximumStream int64         `mapstructure:"maximum_stream" json:"maximum_stream" yaml:"maximum_stream"`
}

func NewScannerOptions() *ScannerOptions {
	return &ScannerOptions{
		Address: "127.0.0.1:3310", DialTimeout: 3 * time.Second,
		ScanTimeout: 30 * time.Second, MaximumStream: 64 << 20,
	}
}

func (o ScannerOptions) Validate() error {
	var joined error
	host, port, err := net.SplitHostPort(strings.TrimSpace(o.Address))
	if err != nil || host == "" || port == "" {
		joined = errors.Join(joined, errors.New("scanner.address must be a host:port endpoint"))
	}
	if o.DialTimeout <= 0 {
		joined = errors.Join(joined, errors.New("scanner.dial_timeout must be positive"))
	}
	if o.ScanTimeout <= 0 {
		joined = errors.Join(joined, errors.New("scanner.scan_timeout must be positive"))
	}
	if o.MaximumStream < 1 || o.MaximumStream > 1<<30 {
		joined = errors.Join(joined, errors.New("scanner.maximum_stream must be between 1 byte and 1 GiB"))
	}
	return joined
}

type CollaborationOptions struct {
	BaseURL        string            `mapstructure:"base_url" json:"base_url" yaml:"base_url"`
	RequestTimeout time.Duration     `mapstructure:"request_timeout" json:"request_timeout" yaml:"request_timeout"`
	TLS            option.TLSOptions `mapstructure:"tls" json:"tls" yaml:"tls"`
}

func NewCollaborationOptions() *CollaborationOptions {
	return &CollaborationOptions{BaseURL: "http://127.0.0.1:8092", RequestTimeout: 5 * time.Second}
}

func (o CollaborationOptions) Validate() error {
	parsed, err := url.Parse(o.BaseURL)
	var endpointErr error
	if err != nil || parsed == nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") || (parsed.Path != "" && parsed.Path != "/") {
		endpointErr = errors.New("collaboration.base_url must be an absolute HTTP origin without credentials")
	} else if parsed.Scheme == "https" != o.TLS.Enabled {
		endpointErr = errors.New("collaboration TLS settings must match the base_url scheme")
	}
	var timeoutErr error
	if o.RequestTimeout <= 0 {
		timeoutErr = errors.New("collaboration.request_timeout must be positive")
	}
	return errors.Join(endpointErr, timeoutErr, o.TLS.Validate())
}

type WorkerOptions struct {
	PollInterval     time.Duration `mapstructure:"poll_interval" json:"poll_interval" yaml:"poll_interval"`
	OperationTimeout time.Duration `mapstructure:"operation_timeout" json:"operation_timeout" yaml:"operation_timeout"`
}

func NewWorkerOptions() *WorkerOptions {
	return &WorkerOptions{
		PollInterval: time.Second, OperationTimeout: 30 * time.Second,
	}
}

func (o WorkerOptions) Validate() error {
	var joined error
	for name, value := range map[string]time.Duration{
		"poll_interval": o.PollInterval, "operation_timeout": o.OperationTimeout,
	} {
		if value <= 0 {
			joined = errors.Join(joined, fmt.Errorf("workers.%s must be positive", name))
		}
	}
	return joined
}
