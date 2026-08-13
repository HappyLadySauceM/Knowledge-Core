package main

import (
	"context"
	"fmt"

	coreapp "github.com/HappyLadySauce/Knowledge-Core/pkg/app"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/config"
	servicecontext "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/context"
)

const serviceName = "knowledge"

func knowledgeSpec() coreapp.Spec[config.Config] {
	provider := config.NewProvider()
	return coreapp.Spec[config.Config]{
		Name:           serviceName,
		Config:         provider,
		RuntimeOptions: knowledgeRuntimeOptions,
		Register: func(ctx context.Context, cfg config.Config, runtime *coreapp.Runtime) error {
			service, err := servicecontext.NewServiceContext(ctx, cfg, runtime)
			if err == nil {
				provider.BindServiceApplier(service.ApplyDynamicConfig)
			}
			return err
		},
	}
}

func knowledgeRuntimeOptions(cfg config.Config) (coreapp.RuntimeOptions, error) {
	if err := cfg.Validate(); err != nil {
		return coreapp.RuntimeOptions{}, err
	}
	options, err := coreapp.RuntimeOptionsFrom(cfg.App, cfg.Log, cfg.Trace)
	if err != nil {
		return coreapp.RuntimeOptions{}, fmt.Errorf("configure knowledge runtime: %w", err)
	}
	return options, nil
}
