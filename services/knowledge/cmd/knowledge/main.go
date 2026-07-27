package main

import (
	"context"
	"os"

	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/observability"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/bootstrap"
)

func main() {
	if err := bootstrap.Run(context.Background()); err != nil {
		observability.NewJSONLogger(os.Stderr, "error", "knowledge").Error("knowledge failed", "error", err)
		os.Exit(1)
	}
}
