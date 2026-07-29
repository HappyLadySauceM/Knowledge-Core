package nats

import (
	"context"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/internal/messaging"
)

func TestValidationRejectsIncompleteContracts(t *testing.T) {
	if err := validateMessage(messaging.Message{}); err == nil {
		t.Fatal("validateMessage() accepted an empty subject")
	}
	if err := validateConsumer(messaging.ConsumerConfig{}, func(_ context.Context, _ messaging.Delivery) {}); err == nil {
		t.Fatal("validateConsumer() accepted an incomplete config")
	}
}
