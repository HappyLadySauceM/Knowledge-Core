package router

import (
	"errors"
	"log/slog"

	"github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/middleware"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
)

type Config struct {
	Logger       *slog.Logger
	Tracing      app.HandlerFunc
	Verifier     middleware.TokenVerifier
	Dependencies middleware.RuntimeDependencies
}

func Register(engine *server.Hertz, cfg Config) error {
	switch {
	case engine == nil:
		return errors.New("register gateway routes: engine is required")
	case cfg.Logger == nil:
		return errors.New("register gateway routes: logger is required")
	case cfg.Tracing == nil:
		return errors.New("register gateway routes: tracing middleware is required")
	case cfg.Verifier == nil:
		return errors.New("register gateway routes: token verifier is required")
	case cfg.Dependencies.Health == nil:
		return errors.New("register gateway routes: health registry is required")
	case cfg.Dependencies.Identity == nil:
		return errors.New("register gateway routes: Identity client is required")
	case cfg.Dependencies.Knowledge == nil:
		return errors.New("register gateway routes: Knowledge client is required")
	}
	engine.Use(
		middleware.RequestID(),
		cfg.Tracing,
		middleware.AccessLog(cfg.Logger),
		middleware.Authentication(cfg.Verifier),
		middleware.Dependencies(cfg.Dependencies),
	)
	GeneratedRegister(engine)
	return nil
}
