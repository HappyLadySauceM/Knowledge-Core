package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	platformv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/platform"
	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	"github.com/HappyLadySauce/Knowledge-Core/services/platform/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/platform/internal/repository"
)

type Store interface {
	Get(context.Context, string) (domain.Snapshot, error)
	GetRevision(context.Context, string, int64) (domain.Snapshot, error)
	Put(context.Context, repository.PutRequest) (domain.Snapshot, error)
	GetDelivery(context.Context, string, int64) (repository.Delivery, error)
	ConsumerState(context.Context, string, string) (domain.ConsumerState, error)
	ReportDelivery(context.Context, domain.DeliveryUpdate) error
}

type Service struct{ store Store }

func New(store Store) (*Service, error) {
	if store == nil {
		return nil, errors.New("create platform service: store is required")
	}
	return &Service{store: store}, nil
}

func (s *Service) Get(ctx context.Context, namespace string) (*platformv1.Configuration, error) {
	if _, err := domain.Default(strings.TrimSpace(namespace)); err != nil {
		return nil, err
	}
	snapshot, err := s.store.Get(ctx, strings.TrimSpace(namespace))
	if err != nil {
		return nil, err
	}
	return toConfiguration(snapshot), nil
}

func (s *Service) Put(ctx context.Context, actorID int64, request *platformv1.PutConfigurationRequest) (*platformv1.Configuration, error) {
	if actorID <= 0 || request == nil || request.ExpectedRevision < 0 || !validIdempotencyKey(request.IdempotencyKey) || len(request.Values) == 0 || len(request.Values) > 32 {
		return nil, domain.ErrInvalid
	}
	namespace := strings.TrimSpace(request.Namespace)
	if _, err := domain.Default(namespace); err != nil {
		return nil, err
	}
	hash, err := requestHash(namespace, request.ExpectedRevision, request.Values)
	if err != nil {
		return nil, err
	}
	snapshot, err := s.store.Put(ctx, repository.PutRequest{ActorID: actorID, Namespace: namespace, ExpectedRevision: request.ExpectedRevision, IdempotencyKey: request.IdempotencyKey, RequestHash: hash, Values: request.Values})
	if err != nil {
		return nil, err
	}
	return toConfiguration(snapshot), nil
}

func (s *Service) SiteProfile(ctx context.Context) (*platformv1.SiteProfile, error) {
	snapshot, err := s.store.Get(ctx, "site")
	if err != nil {
		return nil, err
	}
	x, err := strconv.ParseFloat(snapshot.Public["hero_focal_x"], 64)
	if err != nil {
		return nil, fmt.Errorf("decode site hero focal X: %w", err)
	}
	y, err := strconv.ParseFloat(snapshot.Public["hero_focal_y"], 64)
	if err != nil {
		return nil, fmt.Errorf("decode site hero focal Y: %w", err)
	}
	result := &platformv1.SiteProfile{Title: snapshot.Public["title"], TaglineZh: snapshot.Public["tagline_zh"], TaglineEn: snapshot.Public["tagline_en"], HeroImageUrl: snapshot.Public["hero_image_url"], HeroFocalX: x, HeroFocalY: y, Revision: snapshot.Revision}
	if attachmentID := snapshot.Public["hero_attachment_id"]; attachmentID != "" {
		result.HeroAttachmentId = &attachmentID
	}
	return result, nil
}

func (s *Service) Delivery(ctx context.Context, namespace string, revision int64) (*platformv1.ConfigurationDelivery, error) {
	if _, err := domain.Default(strings.TrimSpace(namespace)); err != nil || revision <= 0 {
		return nil, domain.ErrInvalid
	}
	delivery, err := s.store.GetDelivery(ctx, strings.TrimSpace(namespace), revision)
	if err != nil {
		return nil, err
	}
	result := &platformv1.ConfigurationDelivery{MessageId: delivery.MessageID, Namespace: delivery.Namespace, Revision: delivery.Revision, Status: delivery.Status, Attempts: int32(delivery.Attempts)}
	if delivery.LastErrorKey != "" {
		result.LastErrorKey = &delivery.LastErrorKey
	}
	if delivery.PublishedAt != nil {
		value := delivery.PublishedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
		result.PublishedAt = &value
	}
	return result, nil
}

func (s *Service) ConsumerConfiguration(ctx context.Context, namespace string, revision int64, consumer string) (*platformv1.Configuration, error) {
	if !validConsumer(consumer) || revision <= 0 {
		return nil, domain.ErrInvalid
	}
	if _, err := domain.Default(strings.TrimSpace(namespace)); err != nil {
		return nil, err
	}
	snapshot, err := s.store.GetRevision(ctx, strings.TrimSpace(namespace), revision)
	if err != nil {
		return nil, err
	}
	return toConsumerConfiguration(snapshot), nil
}

func (s *Service) ConsumerState(ctx context.Context, namespace, consumer string) (*platformv1.ConsumerConfigurationState, error) {
	if !validConsumer(consumer) {
		return nil, domain.ErrInvalid
	}
	namespace = strings.TrimSpace(namespace)
	if _, err := domain.Default(namespace); err != nil {
		return nil, err
	}
	state, err := s.store.ConsumerState(ctx, namespace, consumer)
	if err != nil {
		return nil, err
	}
	result := &platformv1.ConsumerConfigurationState{Environment: state.Environment, Namespace: state.Namespace, Consumer: state.Consumer, DesiredRevision: state.DesiredRevision, AppliedRevision: state.AppliedRevision, Status: state.Status}
	if state.LastErrorKey != "" {
		result.LastErrorKey = &state.LastErrorKey
	}
	return result, nil
}

func (s *Service) ReportConsumerApply(ctx context.Context, request *platformv1.ReportConfigurationApplyRequest) error {
	if request == nil || !validConsumer(request.Consumer) || request.Revision <= 0 || request.MessageId == "" || request.Attempts < 0 {
		return domain.ErrInvalid
	}
	namespace := strings.TrimSpace(request.Namespace)
	if _, err := domain.Default(namespace); err != nil {
		return err
	}
	return s.store.ReportDelivery(ctx, domain.DeliveryUpdate{MessageID: request.MessageId, Namespace: namespace, Revision: request.Revision, Consumer: request.Consumer, Status: request.Status, Attempts: int(request.Attempts), LastErrorKey: request.GetLastErrorKey()})
}

func toConfiguration(snapshot domain.Snapshot) *platformv1.Configuration {
	keys := make([]string, 0, len(snapshot.Public))
	for key := range snapshot.Public {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]*platformv1.ConfigValue, 0, len(keys)+len(domain.SecretKeys(snapshot.Namespace)))
	for _, key := range keys {
		values = append(values, &platformv1.ConfigValue{Key: key, Value: snapshot.Public[key]})
	}
	for _, key := range domain.SecretKeys(snapshot.Namespace) {
		_, configured := snapshot.Secrets[key]
		values = append(values, &platformv1.ConfigValue{Key: key, Secret: true, Redacted: configured})
	}
	updatedAt := ""
	if !snapshot.UpdatedAt.IsZero() {
		updatedAt = snapshot.UpdatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	return &platformv1.Configuration{Environment: snapshot.Environment, Namespace: snapshot.Namespace, Revision: snapshot.Revision, SchemaVersion: snapshot.SchemaVersion, Values: values, UpdatedAt: updatedAt, UpdatedBy: snapshot.UpdatedBy}
}

func toConsumerConfiguration(snapshot domain.Snapshot) *platformv1.Configuration {
	keys := make([]string, 0, len(snapshot.Public)+len(snapshot.Secrets))
	for key := range snapshot.Public {
		keys = append(keys, key)
	}
	for key := range snapshot.Secrets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]*platformv1.ConfigValue, 0, len(keys))
	for _, key := range keys {
		value, secret := snapshot.Public[key], false
		if candidate, ok := snapshot.Secrets[key]; ok {
			value, secret = candidate, true
		}
		values = append(values, &platformv1.ConfigValue{Key: key, Value: value, Secret: secret})
	}
	return &platformv1.Configuration{Environment: snapshot.Environment, Namespace: snapshot.Namespace, Revision: snapshot.Revision, SchemaVersion: snapshot.SchemaVersion, Values: values, UpdatedAt: snapshot.UpdatedAt.UTC().Format(time.RFC3339Nano), UpdatedBy: snapshot.UpdatedBy}
}

func validConsumer(value string) bool {
	switch strings.TrimSpace(value) {
	case "identity.email":
		return true
	default:
		return false
	}
}

func requestHash(namespace string, revision int64, values map[string]string) (string, error) {
	encoded, err := jsoncodec.Marshal(map[string]any{"namespace": namespace, "expected_revision": revision, "values": values})
	if err != nil {
		return "", fmt.Errorf("encode configuration request hash: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func validIdempotencyKey(value string) bool {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if char < 0x21 || char > 0x7e {
			return false
		}
	}
	return true
}
