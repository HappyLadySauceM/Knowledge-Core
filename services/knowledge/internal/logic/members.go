package logic

import (
	"context"
	"errors"
	"strings"

	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/repository"
)

type MemberRepository interface {
	ListMembers(context.Context, string, int64) ([]*domain.Member, error)
	AddMember(context.Context, string, int64, domain.PublicUser, string, repository.Idempotency) (*domain.Member, error)
	UpdateMember(context.Context, string, int64, int64, int64, string) (*domain.Member, error)
	DeleteMember(context.Context, string, int64, int64, int64) error
}

type MemberLogic struct {
	repository MemberRepository
	directory  Directory
}

type AddMemberInput struct {
	DocumentID     string
	ActorID        int64
	Username       string
	Role           string
	IdempotencyKey string
}

func NewMemberLogic(repository MemberRepository, directory Directory) (*MemberLogic, error) {
	if repository == nil || directory == nil {
		return nil, errors.New("create member logic: repository and directory are required")
	}
	return &MemberLogic{repository: repository, directory: directory}, nil
}

func (l *MemberLogic) List(ctx context.Context, documentID string, actorID int64) ([]*domain.Member, error) {
	if err := domain.ValidateID("document_id", documentID); err != nil {
		return nil, mapError(err)
	}
	result, err := l.repository.ListMembers(ctx, documentID, actorID)
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func (l *MemberLogic) Add(ctx context.Context, input AddMemberInput) (*domain.Member, error) {
	if err := domain.ValidateID("document_id", input.DocumentID); err != nil {
		return nil, mapError(err)
	}
	input.Username = strings.TrimSpace(input.Username)
	if input.Username == "" || len(input.Username) > 32 {
		return nil, mapError(&domain.ValidationError{Field: "username", Reason: "must contain between 1 and 32 characters"})
	}
	if err := domain.ValidateRole(input.Role); err != nil {
		return nil, mapError(err)
	}
	value, err := idempotency(input.ActorID, "add_member", input.IdempotencyKey, struct {
		DocumentID string `json:"document_id"`
		Username   string `json:"username"`
		Role       string `json:"role"`
	}{input.DocumentID, strings.ToLower(input.Username), input.Role})
	if err != nil {
		return nil, mapError(err)
	}
	user, err := l.directory.ResolveUser(ctx, input.Username)
	if err != nil {
		return nil, mapError(err)
	}
	member, err := l.repository.AddMember(ctx, input.DocumentID, input.ActorID, user, input.Role, value)
	if err != nil {
		return nil, mapError(err)
	}
	return member, nil
}

func (l *MemberLogic) Update(ctx context.Context, documentID string, actorID, userID, expected int64, role string) (*domain.Member, error) {
	if err := validateMemberMutation(documentID, userID, expected, role); err != nil {
		return nil, mapError(err)
	}
	result, err := l.repository.UpdateMember(ctx, documentID, actorID, userID, expected, role)
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func (l *MemberLogic) Delete(ctx context.Context, documentID string, actorID, userID, expected int64) error {
	if err := validateMemberMutation(documentID, userID, expected, domain.AccessViewer); err != nil {
		return mapError(err)
	}
	if err := l.repository.DeleteMember(ctx, documentID, actorID, userID, expected); err != nil {
		return mapError(err)
	}
	return nil
}

func validateMemberMutation(documentID string, userID, expected int64, role string) error {
	if err := domain.ValidateID("document_id", documentID); err != nil {
		return err
	}
	if userID <= 0 {
		return &domain.ValidationError{Field: "user_id", Reason: "must be positive"}
	}
	if expected <= 0 {
		return &domain.ValidationError{Field: "expected_revision", Reason: "must be positive"}
	}
	return domain.ValidateRole(role)
}
