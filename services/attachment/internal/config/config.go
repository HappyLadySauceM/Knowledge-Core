package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
)

type AuthOptions struct {
	PublicKey string `mapstructure:"public_key" json:"public_key" yaml:"public_key"`
}

func (o AuthOptions) Validate() error {
	if strings.TrimSpace(o.PublicKey) == "" {
		return errors.New("auth.public_key is required")
	}
	return nil
}

type ObjectStorageOptions struct {
	Endpoint         string        `mapstructure:"endpoint" json:"endpoint" yaml:"endpoint"`
	PublicEndpoint   string        `mapstructure:"public_endpoint" json:"public_endpoint" yaml:"public_endpoint"`
	Region           string        `mapstructure:"region" json:"region" yaml:"region"`
	Bucket           string        `mapstructure:"bucket" json:"bucket" yaml:"bucket"`
	AccessKey        string        `mapstructure:"access_key" json:"access_key" yaml:"access_key"`
	SecretKey        string        `mapstructure:"secret_key" json:"secret_key" yaml:"secret_key"`
	Secure           bool          `mapstructure:"secure" json:"secure" yaml:"secure"`
	PublicSecure     bool          `mapstructure:"public_secure" json:"public_secure" yaml:"public_secure"`
	AutoCreateBucket bool          `mapstructure:"auto_create_bucket" json:"auto_create_bucket" yaml:"auto_create_bucket"`
	UploadTTL        time.Duration `mapstructure:"upload_ttl" json:"upload_ttl" yaml:"upload_ttl"`
	DownloadTTL      time.Duration `mapstructure:"download_ttl" json:"download_ttl" yaml:"download_ttl"`
}

func (o ObjectStorageOptions) Validate() error {
	var joined error
	for name, value := range map[string]string{"endpoint": o.Endpoint, "public_endpoint": o.PublicEndpoint, "region": o.Region, "bucket": o.Bucket, "access_key": o.AccessKey, "secret_key": o.SecretKey} {
		if strings.TrimSpace(value) == "" {
			joined = errors.Join(joined, fmt.Errorf("object_storage.%s is required", name))
		}
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

func (o ScannerOptions) Validate() error {
	if strings.TrimSpace(o.Address) == "" || o.DialTimeout <= 0 || o.ScanTimeout <= 0 || o.MaximumStream < 1 {
		return errors.New("scanner configuration is invalid")
	}
	return nil
}

type Config struct {
	App           *option.AppOptions         `mapstructure:"app" json:"app" yaml:"app"`
	Log           *option.LogOptions         `mapstructure:"log" json:"log" yaml:"log"`
	Trace         *option.TraceOptions       `mapstructure:"trace" json:"trace" yaml:"trace"`
	RPC           *option.KitexServerOptions `mapstructure:"rpc" json:"rpc" yaml:"rpc"`
	AdminHTTP     *option.HertzServerOptions `mapstructure:"admin_http" json:"admin_http" yaml:"admin_http"`
	PostgreSQL    *option.PostgreSQLOptions  `mapstructure:"postgres" json:"postgres" yaml:"postgres"`
	Auth          *AuthOptions               `mapstructure:"auth" json:"auth" yaml:"auth"`
	ObjectStorage *ObjectStorageOptions      `mapstructure:"object_storage" json:"object_storage" yaml:"object_storage"`
	Scanner       *ScannerOptions            `mapstructure:"scanner" json:"scanner" yaml:"scanner"`
}

func New() Config {
	app := option.NewAppOptions("attachment")
	app.ShutdownTimeout = 45 * time.Second
	rpc := option.NewKitexServerOptions()
	rpc.Address = ":8884"
	rpc.ServiceName = "knowledge-core.attachment"
	admin := option.NewHertzServerOptions()
	admin.Address = ":8085"
	return Config{App: app, Log: option.NewLogOptions(), Trace: option.NewTraceOptions(), RPC: rpc, AdminHTTP: admin, PostgreSQL: option.NewPostgreSQLOptions(), Auth: &AuthOptions{}, ObjectStorage: &ObjectStorageOptions{Endpoint: "127.0.0.1:9000", PublicEndpoint: "127.0.0.1:9000", Region: "us-east-1", Bucket: "knowledge-core-attachments", UploadTTL: 24 * time.Hour, DownloadTTL: 5 * time.Minute}, Scanner: &ScannerOptions{Address: "127.0.0.1:3310", DialTimeout: 3 * time.Second, ScanTimeout: 20 * time.Minute, MaximumStream: 1 << 30}}
}
func (c Config) Validate() error {
	if c.App == nil || c.Log == nil || c.Trace == nil || c.RPC == nil || c.AdminHTTP == nil || c.PostgreSQL == nil || c.Auth == nil || c.ObjectStorage == nil || c.Scanner == nil {
		return errors.New("all attachment configuration sections are required")
	}
	return errors.Join(c.App.Validate(), c.Log.Validate(), c.Trace.Validate(), c.RPC.Validate(), c.AdminHTTP.Validate(), c.PostgreSQL.Validate(), c.Auth.Validate(), c.ObjectStorage.Validate(), c.Scanner.Validate())
}
