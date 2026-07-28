package main

import (
	"os"

	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/command"
	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/observability"
	"github.com/HappyLadySauce/Knowledge-Core/services/platform/internal/bootstrap"
)

func main() {
	if err := command.Execute("platform", bootstrap.Run); err != nil {
		observability.NewBootstrapLogger(os.Stderr, "platform").Error("platform failed", "error", err)
		os.Exit(1)
	}
}
