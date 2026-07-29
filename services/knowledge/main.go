package main

import (
	"os"

	"github.com/HappyLadySauce/Knowledge-Core/internal/command"
	"github.com/HappyLadySauce/Knowledge-Core/internal/observability"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/bootstrap"
)

func main() {
	if err := command.Execute("knowledge", bootstrap.Run); err != nil {
		observability.NewBootstrapLogger(os.Stderr, "knowledge").Error("knowledge failed", "error", err)
		os.Exit(1)
	}
}
