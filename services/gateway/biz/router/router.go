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
	Middleware   middleware.Config
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
	case cfg.Dependencies.Cache == nil:
		return errors.New("register gateway routes: cache store is required")
	case cfg.Dependencies.Identity == nil:
		return errors.New("register gateway routes: Identity client is required")
	case cfg.Dependencies.Knowledge == nil:
		return errors.New("register gateway routes: Knowledge client is required")
	}
	if err := cfg.Middleware.Validate(); err != nil {
		return err
	}

	engine.PanicHandler = middleware.JSONPanicHandler(cfg.Logger)
	handlers := []app.HandlerFunc{middleware.RequestID()}
	handlers = append(handlers, middleware.Trace(cfg.Tracing)...)
	handlers = append(handlers,
		middleware.AccessLog(cfg.Logger),
		middleware.JSONRecovery(cfg.Logger),
		middleware.SecurityHeaders(),
		middleware.CORS(cfg.Middleware.CORS, cfg.Middleware.TrustedProxyCIDRs),
		middleware.RateLimit(cfg.Dependencies.Cache, cfg.Middleware.RateLimit),
		middleware.Authentication(cfg.Verifier),
		middleware.Dependencies(cfg.Dependencies),
	)
	engine.Use(handlers...)
	GeneratedRegister(engine)
	engine.NoRoute(middleware.NoRoute())
	engine.NoMethod(middleware.NoMethod())
	return nil
}
