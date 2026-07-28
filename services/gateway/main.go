package main

import (
	"os"

	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/command"
	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/observability"
	"github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/bootstrap"
)

func main() {
	if err := command.Execute("gateway", bootstrap.Run); err != nil {
		observability.NewBootstrapLogger(os.Stderr, "gateway").Error("gateway failed", "error", err)
		os.Exit(1)
	}
}
