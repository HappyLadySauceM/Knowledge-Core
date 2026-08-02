package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/app"
	corelog "github.com/HappyLadySauce/Knowledge-Core/pkg/log"
)

func main() {
	command := app.NewAPICommand(context.Background(), knowledgeSpec())
	if err := command.Execute(); err != nil {
		corelog.NewBootstrap(serviceName, os.Stderr).Error(
			"application failed",
			slog.String("component", "main"),
			slog.String("event", "exit"),
			slog.Any("error", err),
		)
		os.Exit(1)
	}
}
