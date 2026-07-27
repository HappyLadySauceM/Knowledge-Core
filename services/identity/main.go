package main

import (
	"context"
	"os"

	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/observability"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/bootstrap"
)

func main() {
	if err := bootstrap.Run(context.Background()); err != nil {
		observability.NewJSONLogger(os.Stderr, "error", "identity").Error("identity failed", "error", err)
		os.Exit(1)
	}
}
