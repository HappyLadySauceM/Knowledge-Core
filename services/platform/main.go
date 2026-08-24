package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/app"
	corelog "github.com/HappyLadySauce/Knowledge-Core/pkg/log"
)

func main() {
	if err := app.NewAPICommand(context.Background(), platformSpec()).Execute(); err != nil {
		corelog.NewBootstrap(serviceName, os.Stderr).Error("application failed", slog.String("component", "main"), slog.Any("error", err))
		os.Exit(1)
	}
}
