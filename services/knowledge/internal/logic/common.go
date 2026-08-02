package logic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	apperror "github.com/HappyLadySauce/Knowledge-Core/pkg/error"
	knowledgeclient "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/client"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	knowledgeerrors "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/errors"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/repository"
)

type Directory interface {
	CurrentUser(ctx context.Context) (domain.PublicUser, error)
	ResolveUser(ctx context.Context, username string) (domain.PublicUser, error)
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := apperror.Details(err); ok {
		return err
	}
	var validationError *domain.ValidationError
	switch {
	case errors.As(err, &validationError):
		return knowledgeerrors.InvalidInput.Wrap(err)
	case errors.Is(err, repository.ErrNotFound), errors.Is(err, knowledgeclient.ErrDirectoryNotFound):
		return knowledgeerrors.NotFound.Wrap(err)
	case errors.Is(err, repository.ErrGone):
		return knowledgeerrors.Gone.Wrap(err)
	case errors.Is(err, repository.ErrForbidden):
		return knowledgeerrors.Forbidden.Wrap(err)
	case errors.Is(err, repository.ErrPrecondition):
		return knowledgeerrors.Precondition.Wrap(err)
	case errors.Is(err, repository.ErrQuotaExceeded):
		return knowledgeerrors.QuotaExceeded.Wrap(err)
	case errors.Is(err, repository.ErrConflict):
		return knowledgeerrors.Conflict.Wrap(err)
	case errors.Is(err, knowledgeclient.ErrDirectoryUnauthorized):
		return knowledgeerrors.Unauthenticated.Wrap(err)
	case errors.Is(err, knowledgeclient.ErrDirectoryUnavailable):
		return knowledgeerrors.Unavailable.Wrap(err)
	default:
		return knowledgeerrors.Internal.Wrap(err)
	}
}

func idempotency(actorID int64, operation, key string, request any) (repository.Idempotency, error) {
	key = strings.TrimSpace(key)
	if err := domain.ValidateIdempotencyKey(key); err != nil {
		return repository.Idempotency{}, err
	}
	if key == "" {
		return repository.Idempotency{}, nil
	}
	payload, err := jsoncodec.Marshal(request)
	if err != nil {
		return repository.Idempotency{}, fmt.Errorf("hash idempotent request: %w", err)
	}
	digest := sha256.Sum256(payload)
	return repository.Idempotency{
		ActorID: actorID, Operation: operation, Key: key, RequestHash: hex.EncodeToString(digest[:]),
	}, nil
}

func effectiveLimit(value int32) int {
	if value <= 0 {
		return 20
	}
	return int(value)
}
