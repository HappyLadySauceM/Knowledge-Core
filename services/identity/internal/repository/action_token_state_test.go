package repository

import (
	"errors"
	"testing"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/model"
)

func TestClassifyActionToken(t *testing.T) {
	now := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	usedAt := now.Add(-time.Minute)
	cases := []struct {
		name    string
		action  *model.ActionToken
		wantErr error
	}{
		{name: "missing", wantErr: ErrActionNotFound},
		{
			name:    "already used",
			action:  &model.ActionToken{UsedAt: &usedAt, ExpiresAt: now.Add(time.Hour)},
			wantErr: ErrActionAlreadyUsed,
		},
		{
			name:    "expired",
			action:  &model.ActionToken{ExpiresAt: now},
			wantErr: ErrActionExpired,
		},
		{
			name:   "usable",
			action: &model.ActionToken{ExpiresAt: now.Add(time.Minute)},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := classifyActionToken(test.action, now)
			if test.wantErr == nil {
				if err != nil {
					t.Fatalf("classifyActionToken() error = %v", err)
				}
				return
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("classifyActionToken() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}
