package main

import (
	"os"

	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/command"
	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/observability"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/bootstrap"
)

func main() {
	if err := command.Execute("identity", bootstrap.Run); err != nil {
		observability.NewBootstrapLogger(os.Stderr, "identity").Error("identity failed", "error", err)
		os.Exit(1)
	}
}
