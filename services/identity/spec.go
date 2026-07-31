package main

import (
	"context"
	"fmt"

	coreapp "github.com/HappyLadySauce/Knowledge-Core/pkg/app"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/config"
	servicecontext "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/context"
)

const serviceName = "identity"

func identitySpec() coreapp.Spec[config.Config] {
	return coreapp.Spec[config.Config]{
		Name:           serviceName,
		Config:         config.NewProvider(),
		RuntimeOptions: identityRuntimeOptions,
		Register: func(ctx context.Context, cfg config.Config, runtime *coreapp.Runtime) error {
			_, err := servicecontext.NewServiceContext(ctx, cfg, runtime)
			return err
		},
	}
}

func identityRuntimeOptions(cfg config.Config) (coreapp.RuntimeOptions, error) {
	if err := cfg.Validate(); err != nil {
		return coreapp.RuntimeOptions{}, err
	}
	options, err := coreapp.RuntimeOptionsFrom(cfg.App, cfg.Log, cfg.Trace)
	if err != nil {
		return coreapp.RuntimeOptions{}, fmt.Errorf("configure identity runtime: %w", err)
	}
	return options, nil
}
