package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
)

type AuthOptions struct {
	PublicKey              string `mapstructure:"public_key" json:"public_key" yaml:"public_key"`
	AttachmentServiceToken string `mapstructure:"attachment_service_token" json:"attachment_service_token" yaml:"attachment_service_token"`
}

func NewAuthOptions() *AuthOptions { return &AuthOptions{} }

func (o AuthOptions) Validate() error {
	if _, err := coreauth.NewVerifier(o.PublicKey); err != nil {
		return err
	}
	if strings.TrimSpace(o.AttachmentServiceToken) == "" {
		return errors.New("attachment_service_token is required")
	}
	return nil
}

type WorkerOptions struct {
	PollInterval     time.Duration `mapstructure:"poll_interval" json:"poll_interval" yaml:"poll_interval"`
	OperationTimeout time.Duration `mapstructure:"operation_timeout" json:"operation_timeout" yaml:"operation_timeout"`
}

func NewWorkerOptions() *WorkerOptions {
	return &WorkerOptions{
		PollInterval: 30 * time.Second, OperationTimeout: 30 * time.Second,
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
