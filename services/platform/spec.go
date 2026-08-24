package main

import (
	"context"
	"fmt"

	coreapp "github.com/HappyLadySauce/Knowledge-Core/pkg/app"
	"github.com/HappyLadySauce/Knowledge-Core/services/platform/internal/config"
	servicecontext "github.com/HappyLadySauce/Knowledge-Core/services/platform/internal/context"
)

const serviceName = "platform"

func platformSpec() coreapp.Spec[config.Config] {
	provider := config.NewProvider()
	return coreapp.Spec[config.Config]{
		Name:           serviceName,
		Config:         provider,
		RuntimeOptions: platformRuntimeOptions,
		Register: func(ctx context.Context, cfg config.Config, runtime *coreapp.Runtime) error {
			_, err := servicecontext.NewServiceContext(ctx, cfg, runtime)
			return err
		},
	}
}

func platformRuntimeOptions(cfg config.Config) (coreapp.RuntimeOptions, error) {
	if err := cfg.Validate(); err != nil {
		return coreapp.RuntimeOptions{}, err
	}
	opts, err := coreapp.RuntimeOptionsFrom(cfg.App, cfg.Log, cfg.Trace)
	if err != nil {
		return coreapp.RuntimeOptions{}, fmt.Errorf("configure platform runtime: %w", err)
	}
	return opts, nil
}
