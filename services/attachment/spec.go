package main

import (
	"context"
	"fmt"

	coreapp "github.com/HappyLadySauce/Knowledge-Core/pkg/app"
	"github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/config"
	servicecontext "github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/context"
)

const serviceName = "attachment"

func attachmentSpec() coreapp.Spec[config.Config] {
	provider := config.NewProvider()
	return coreapp.Spec[config.Config]{Name: serviceName, Config: provider, RuntimeOptions: attachmentRuntimeOptions,
		Register: func(ctx context.Context, cfg config.Config, runtime *coreapp.Runtime) error {
			service, err := servicecontext.NewServiceContext(ctx, cfg, runtime)
			if err == nil {
				provider.BindServiceApplier(service.ApplyDynamicConfig)
			}
			return err
		},
	}
}

func attachmentRuntimeOptions(cfg config.Config) (coreapp.RuntimeOptions, error) {
	if err := cfg.Validate(); err != nil {
		return coreapp.RuntimeOptions{}, err
	}
	opts, err := coreapp.RuntimeOptionsFrom(cfg.App, cfg.Log, cfg.Trace)
	if err != nil {
		return coreapp.RuntimeOptions{}, fmt.Errorf("configure attachment runtime: %w", err)
	}
	return opts, nil
}
