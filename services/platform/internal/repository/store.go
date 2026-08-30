package repository

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

	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/configcenter"
	coretrace "github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
	"github.com/HappyLadySauce/Knowledge-Core/services/platform/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/platform/internal/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Store struct {
	db          *gorm.DB
	environment string
	subject     string
	keyID       string
	key         []byte
	now         func() time.Time
}

type PutRequest struct {
	ActorID          int64
	Namespace        string
	ExpectedRevision int64
	IdempotencyKey   string
	RequestHash      string
	Values           map[string]string
}

type Delivery struct {
	MessageID    string
	Namespace    string
	Revision     int64
	Status       string
	Attempts     int
	LastErrorKey string
	PublishedAt  *time.Time
}

type ConsumerDelivery struct {
	MessageID    string
	Namespace    string
	Revision     int64
	Consumer     string
	Status       string
	Attempts     int
	LastErrorKey string
	AppliedAt    *time.Time
}

func New(db *gorm.DB, environment, subject, keyID, encodedKey string) (*Store, error) {
	if db == nil || environment == "" || subject == "" || keyID == "" {
		return nil, errors.New("create platform store: database, environment, subject, and key ID are required")
	}
	key, err := configcenter.ParseKey(encodedKey)
	if err != nil {
		return nil, fmt.Errorf("create platform store: %w", err)
	}
	return &Store{db: db, environment: environment, subject: subject, keyID: keyID, key: key, now: time.Now}, nil
}

func (s *Store) Get(ctx context.Context, namespace string) (domain.Snapshot, error) {
	var record model.Configuration
	err := s.db.WithContext(ctx).Where("environment = ? AND namespace = ?", s.environment, namespace).Take(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		result, defaultErr := domain.Default(namespace)
		result.Environment = s.environment
		return result, defaultErr
	}
	if err != nil {
		return domain.Snapshot{}, fmt.Errorf("get configuration: %w", err)
	}
	return s.decode(record)
}

func (s *Store) Put(ctx context.Context, request PutRequest) (domain.Snapshot, error) {
	var result domain.Snapshot
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		lockName := fmt.Sprintf("%s:%d:%s:%s", s.environment, request.ActorID, request.Namespace, request.IdempotencyKey)
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", lockName).Error; err != nil {
			return fmt.Errorf("lock configuration idempotency key: %w", err)
		}
		var prior model.ConfigIdempotency
		priorErr := tx.Where("environment = ? AND actor_id = ? AND namespace = ? AND key = ? AND expires_at > ?", s.environment, request.ActorID, request.Namespace, request.IdempotencyKey, s.now().UTC()).Take(&prior).Error
		if priorErr == nil {
			if prior.RequestHash != request.RequestHash {
				return domain.ErrConflict
			}
			decoded, decodeErr := snapshotFromIdempotency(prior)
			if decodeErr != nil {
				return decodeErr
			}
			result = decoded
			return nil
		}
		if !errors.Is(priorErr, gorm.ErrRecordNotFound) {
			return fmt.Errorf("read configuration idempotency key: %w", priorErr)
		}

		var current model.Configuration
		currentErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("environment = ? AND namespace = ?", s.environment, request.Namespace).Take(&current).Error
		var previous domain.Snapshot
		switch {
		case currentErr == nil:
			if request.ExpectedRevision != current.Revision {
				return domain.ErrPrecondition
			}
			decoded, err := s.decode(current)
			if err != nil {
				return err
			}
			previous = decoded
		case errors.Is(currentErr, gorm.ErrRecordNotFound):
			if request.ExpectedRevision != 0 {
				return domain.ErrPrecondition
			}
			defaults, err := domain.Default(request.Namespace)
			if err != nil {
				return err
			}
			defaults.Environment = s.environment
			previous = defaults
		default:
			return fmt.Errorf("lock configuration: %w", currentErr)
		}

		public, secrets, err := domain.Validate(request.Namespace, request.Values, previous.Secrets)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		revision := previous.Revision + 1
		publicJSON, err := jsoncodec.Marshal(public)
		if err != nil {
			return fmt.Errorf("encode public configuration: %w", err)
		}
		secretJSON, err := jsoncodec.Marshal(secrets)
		if err != nil {
			return fmt.Errorf("encode secret configuration: %w", err)
		}
		var envelope []byte
		if len(secrets) > 0 {
			envelope, err = configcenter.Encrypt(secretJSON, s.key, s.keyID, s.binding(request.Namespace, revision))
			if err != nil {
				return fmt.Errorf("encrypt secret configuration: %w", err)
			}
		}
		createdAt := previous.CreatedAt
		if createdAt.IsZero() {
			createdAt = now
		}
		record := model.Configuration{
			Environment: s.environment, Namespace: request.Namespace, Revision: revision, SchemaVersion: domain.SchemaVersion,
			PublicValues: publicJSON, SecretEnvelope: envelope, UpdatedBy: request.ActorID, CreatedAt: createdAt, UpdatedAt: now,
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "environment"}, {Name: "namespace"}}, DoUpdates: clause.AssignmentColumns([]string{"revision", "schema_version", "public_values", "secret_envelope", "updated_by", "updated_at"})}).Create(&record).Error; err != nil {
			return fmt.Errorf("write configuration: %w", err)
		}
		revisionRecord := model.ConfigurationRevision{
			Environment: s.environment, Namespace: request.Namespace, Revision: revision,
			SchemaVersion: domain.SchemaVersion, PublicValues: publicJSON, SecretEnvelope: envelope,
			UpdatedBy: request.ActorID, CreatedAt: createdAt, UpdatedAt: now,
		}
		if err := tx.Create(&revisionRecord).Error; err != nil {
			return fmt.Errorf("write configuration revision: %w", err)
		}
		previousDigest, err := snapshotDigest(previous.Public, previous.Secrets)
		if err != nil {
			return err
		}
		nextDigest, err := snapshotDigest(public, secrets)
		if err != nil {
			return err
		}
		changedJSON, err := jsoncodec.Marshal(changedKeys(previous.Public, previous.Secrets, public, secrets))
		if err != nil {
			return fmt.Errorf("encode changed configuration keys: %w", err)
		}
		auditID, err := domain.NewID()
		if err != nil {
			return err
		}
		if err := tx.Create(&model.ConfigAudit{ID: auditID, Environment: s.environment, Namespace: request.Namespace, Revision: revision, ActorID: request.ActorID, PreviousDigest: previousDigest, NextDigest: nextDigest, ChangedKeys: changedJSON, CreatedAt: now}).Error; err != nil {
			return fmt.Errorf("write configuration audit: %w", err)
		}
		configuredSecretKeys := make([]string, 0, len(secrets))
		for key := range secrets {
			configuredSecretKeys = append(configuredSecretKeys, key)
		}
		sort.Strings(configuredSecretKeys)
		secretKeyJSON, err := jsoncodec.Marshal(configuredSecretKeys)
		if err != nil {
			return fmt.Errorf("encode configured secret keys: %w", err)
		}
		if err := tx.Create(&model.ConfigIdempotency{Environment: s.environment, ActorID: request.ActorID, Namespace: request.Namespace, Key: request.IdempotencyKey, RequestHash: request.RequestHash, Revision: revision, SchemaVersion: domain.SchemaVersion, ResponsePublicValues: publicJSON, ResponseSecretKeys: secretKeyJSON, ResponseUpdatedAt: now, ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now}).Error; err != nil {
			return fmt.Errorf("write configuration idempotency key: %w", err)
		}
		messageID, err := domain.NewID()
		if err != nil {
			return err
		}
		event := map[string]any{
			"message_id": messageID, "message_type": "platform.config.changed", "schema_version": 1,
			"aggregate_id": s.environment + ":" + request.Namespace, "aggregate_version": revision,
			"environment": s.environment, "namespace": request.Namespace, "value_digest": nextDigest,
			"occurred_at": now.Format(time.RFC3339Nano), "correlation_id": messageID, "causation_id": request.IdempotencyKey,
			"idempotency_key": request.IdempotencyKey, "producer": "knowledge-core.platform",
		}
		payload, err := jsoncodec.Marshal(event)
		if err != nil {
			return fmt.Errorf("encode configuration event: %w", err)
		}
		headers := coretrace.PropagationHeaders(ctx)
		headers["X-Message-Type"] = "platform.config.changed"
		headers["X-Schema-Version"] = "1"
		headers["X-Aggregate-ID"] = s.environment + ":" + request.Namespace
		headers["X-Aggregate-Version"] = strconv.FormatInt(revision, 10)
		headers["X-Causation-ID"] = request.IdempotencyKey
		headerJSON, err := jsoncodec.Marshal(headers)
		if err != nil {
			return fmt.Errorf("encode configuration event headers: %w", err)
		}
		if err := tx.Create(&model.ConfigOutbox{ID: messageID, Environment: s.environment, Namespace: request.Namespace, Revision: revision, Subject: s.subject, Payload: payload, TraceHeaders: headerJSON, NextAttemptAt: now, CreatedAt: now}).Error; err != nil {
			return fmt.Errorf("enqueue configuration event: %w", err)
		}
		result = domain.Snapshot{Environment: s.environment, Namespace: request.Namespace, Revision: revision, SchemaVersion: domain.SchemaVersion, Public: public, Secrets: secrets, UpdatedBy: request.ActorID, CreatedAt: createdAt, UpdatedAt: now}
		return nil
	})
	return result, err
}

func (s *Store) GetRevision(ctx context.Context, namespace string, revision int64) (domain.Snapshot, error) {
	var record model.ConfigurationRevision
	err := s.db.WithContext(ctx).Where("environment = ? AND namespace = ? AND revision = ?", s.environment, namespace, revision).Take(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Snapshot{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Snapshot{}, fmt.Errorf("get configuration revision: %w", err)
	}
	return s.decodeRevision(record)
}

func (s *Store) ConsumerState(ctx context.Context, namespace, consumer string) (domain.ConsumerState, error) {
	var configuration model.Configuration
	lookupErr := s.db.WithContext(ctx).Where("environment = ? AND namespace = ?", s.environment, namespace).Take(&configuration).Error
	state, err := consumerStateAfterConfigurationLookup(s.environment, namespace, consumer, configuration, lookupErr)
	if err != nil {
		return domain.ConsumerState{}, err
	}
	if state.DesiredRevision <= 0 {
		return state, nil
	}
	var latest model.ConfigDelivery
	err = s.db.WithContext(ctx).Where("environment = ? AND namespace = ? AND consumer = ?", s.environment, namespace, consumer).Order("revision DESC").First(&latest).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return state, nil
	}
	if err != nil {
		return domain.ConsumerState{}, fmt.Errorf("get consumer delivery state: %w", err)
	}
	if latest.Revision == state.DesiredRevision {
		state.Status = latest.Status
		state.LastErrorKey = latest.LastErrorKey
	} else if latest.Revision < state.DesiredRevision {
		state.Status = "pending"
	}
	var applied model.ConfigDelivery
	if err := s.db.WithContext(ctx).Where("environment = ? AND namespace = ? AND consumer = ? AND status = ?", s.environment, namespace, consumer, "applied").Order("revision DESC").First(&applied).Error; err == nil {
		state.AppliedRevision = applied.Revision
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.ConsumerState{}, fmt.Errorf("get applied consumer delivery state: %w", err)
	}
	return state, nil
}

func (s *Store) ReportDelivery(ctx context.Context, update domain.DeliveryUpdate) error {
	if update.Revision <= 0 || strings.TrimSpace(update.Namespace) == "" || strings.TrimSpace(update.Consumer) == "" || strings.TrimSpace(update.MessageID) == "" {
		return domain.ErrInvalid
	}
	if update.Status != "validating" && update.Status != "retrying" && update.Status != "applied" && update.Status != "rejected" && update.Status != "parked" {
		return domain.ErrInvalid
	}
	if _, err := uuid.Parse(update.MessageID); err != nil {
		return domain.ErrInvalid
	}
	var revision model.ConfigurationRevision
	if err := s.db.WithContext(ctx).Where("environment = ? AND namespace = ? AND revision = ?", s.environment, update.Namespace, update.Revision).Take(&revision).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.ErrNotFound
	} else if err != nil {
		return fmt.Errorf("verify configuration revision: %w", err)
	}
	now := s.now().UTC()
	var current model.ConfigDelivery
	err := s.db.WithContext(ctx).Where("environment = ? AND namespace = ? AND revision = ? AND consumer = ?", s.environment, update.Namespace, update.Revision, update.Consumer).Take(&current).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		current = model.ConfigDelivery{Environment: s.environment, Namespace: update.Namespace, Revision: update.Revision, Consumer: update.Consumer}
	} else if err != nil {
		return fmt.Errorf("read configuration delivery: %w", err)
	}
	if !acceptDeliveryUpdate(current.Status, update.Status) {
		return nil
	}
	current.MessageID = update.MessageID
	current.Status = update.Status
	current.Attempts = update.Attempts
	current.LastErrorKey = update.LastErrorKey
	current.UpdatedAt = now
	if update.Status == "applied" {
		current.AppliedAt = &now
	}
	if err := s.db.WithContext(ctx).Save(&current).Error; err != nil {
		return fmt.Errorf("write configuration delivery: %w", err)
	}
	return nil
}

func acceptDeliveryUpdate(current, next string) bool {
	if current == "applied" || current == "rejected" || current == "parked" {
		return current == next
	}
	return true
}

func (s *Store) GetDelivery(ctx context.Context, namespace string, revision int64) (Delivery, error) {
	var record model.ConfigOutbox
	if err := s.db.WithContext(ctx).Where("environment = ? AND namespace = ? AND revision = ?", s.environment, namespace, revision).Take(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Delivery{}, domain.ErrNotFound
		}
		return Delivery{}, fmt.Errorf("get configuration delivery: %w", err)
	}
	status := "pending"
	if record.ParkedAt != nil {
		status = "parked"
	} else if record.PublishedAt != nil {
		status = "published"
	}
	return Delivery{MessageID: record.ID, Namespace: namespace, Revision: revision, Status: status, Attempts: record.Attempts, LastErrorKey: record.LastErrorKey, PublishedAt: record.PublishedAt}, nil
}

func (s *Store) ClaimOutbox(ctx context.Context, limit int, lease time.Duration) ([]domain.OutboxMessage, error) {
	if limit < 1 {
		limit = 50
	}
	var result []domain.OutboxMessage
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := s.now().UTC()
		var records []model.ConfigOutbox
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("published_at IS NULL AND parked_at IS NULL AND next_attempt_at <= ?", now).Order("next_attempt_at ASC, created_at ASC, id ASC").Limit(limit).Find(&records).Error; err != nil {
			return fmt.Errorf("claim configuration outbox: %w", err)
		}
		for index := range records {
			if err := tx.Model(&records[index]).Updates(map[string]any{"attempts": gorm.Expr("attempts + 1"), "next_attempt_at": now.Add(lease)}).Error; err != nil {
				return fmt.Errorf("lease configuration outbox: %w", err)
			}
			result = append(result, domain.OutboxMessage{ID: records[index].ID, Subject: records[index].Subject, Payload: append([]byte(nil), records[index].Payload...), Headers: decodeHeaders(records[index].TraceHeaders), Attempts: records[index].Attempts + 1, Namespace: records[index].Namespace, Revision: records[index].Revision})
		}
		return nil
	})
	return result, err
}

func (s *Store) MarkOutboxPublished(ctx context.Context, id string) error {
	result := s.db.WithContext(ctx).Model(&model.ConfigOutbox{}).Where("id = ? AND published_at IS NULL", id).Update("published_at", s.now().UTC())
	if result.Error != nil {
		return fmt.Errorf("mark configuration outbox published: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) RetryOutbox(ctx context.Context, id string, delay time.Duration) error {
	result := s.db.WithContext(ctx).Model(&model.ConfigOutbox{}).Where("id = ? AND published_at IS NULL AND parked_at IS NULL", id).Updates(map[string]any{"next_attempt_at": s.now().UTC().Add(delay), "last_error_key": "publish_failed"})
	if result.Error != nil {
		return fmt.Errorf("retry configuration outbox: %w", result.Error)
	}
	return nil
}

func (s *Store) ParkOutbox(ctx context.Context, id string) error {
	result := s.db.WithContext(ctx).Model(&model.ConfigOutbox{}).Where("id = ? AND published_at IS NULL AND parked_at IS NULL", id).Updates(map[string]any{"parked_at": s.now().UTC(), "last_error_key": "publish_retry_exhausted"})
	if result.Error != nil {
		return fmt.Errorf("park configuration outbox: %w", result.Error)
	}
	return nil
}

func (s *Store) decode(record model.Configuration) (domain.Snapshot, error) {
	var public map[string]string
	if err := jsoncodec.Unmarshal(record.PublicValues, &public); err != nil {
		return domain.Snapshot{}, fmt.Errorf("decode public configuration: %w", err)
	}
	secrets := map[string]string{}
	if len(record.SecretEnvelope) > 0 {
		plaintext, err := configcenter.Decrypt(record.SecretEnvelope, s.key, s.keyID, s.binding(record.Namespace, record.Revision))
		if err != nil {
			return domain.Snapshot{}, fmt.Errorf("decrypt secret configuration: %w", err)
		}
		if err := jsoncodec.Unmarshal(plaintext, &secrets); err != nil {
			return domain.Snapshot{}, fmt.Errorf("decode secret configuration: %w", err)
		}
	}
	return domain.Snapshot{Environment: record.Environment, Namespace: record.Namespace, Revision: record.Revision, SchemaVersion: record.SchemaVersion, Public: public, Secrets: secrets, UpdatedBy: record.UpdatedBy, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}, nil
}

func (s *Store) decodeRevision(record model.ConfigurationRevision) (domain.Snapshot, error) {
	var public map[string]string
	if err := jsoncodec.Unmarshal(record.PublicValues, &public); err != nil {
		return domain.Snapshot{}, fmt.Errorf("decode configuration revision public values: %w", err)
	}
	secrets := map[string]string{}
	if len(record.SecretEnvelope) > 0 {
		plaintext, err := configcenter.Decrypt(record.SecretEnvelope, s.key, s.keyID, s.binding(record.Namespace, record.Revision))
		if err != nil {
			return domain.Snapshot{}, fmt.Errorf("decrypt configuration revision secrets: %w", err)
		}
		if err := jsoncodec.Unmarshal(plaintext, &secrets); err != nil {
			return domain.Snapshot{}, fmt.Errorf("decode configuration revision secrets: %w", err)
		}
	}
	return domain.Snapshot{Environment: record.Environment, Namespace: record.Namespace, Revision: record.Revision, SchemaVersion: record.SchemaVersion, Public: public, Secrets: secrets, UpdatedBy: record.UpdatedBy, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}, nil
}

func (s *Store) binding(namespace string, revision int64) configcenter.Binding {
	return configcenter.Binding{Namespace: s.environment, Group: namespace, DataID: strconv.FormatInt(revision, 10)}
}

func snapshotDigest(public, secrets map[string]string) (string, error) {
	encoded, err := jsoncodec.Marshal(map[string]any{"public": public, "secrets": secrets})
	if err != nil {
		return "", fmt.Errorf("encode configuration digest: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func changedKeys(oldPublic, oldSecrets, nextPublic, nextSecrets map[string]string) []string {
	all := map[string]struct{}{}
	for key := range oldPublic {
		all[key] = struct{}{}
	}
	for key := range oldSecrets {
		all[key] = struct{}{}
	}
	for key := range nextPublic {
		all[key] = struct{}{}
	}
	for key := range nextSecrets {
		all[key] = struct{}{}
	}
	result := make([]string, 0, len(all))
	for key := range all {
		old, oldOK := oldPublic[key]
		if secret, ok := oldSecrets[key]; ok {
			old, oldOK = secret, true
		}
		next, nextOK := nextPublic[key]
		if secret, ok := nextSecrets[key]; ok {
			next, nextOK = secret, true
		}
		if oldOK != nextOK || old != next {
			result = append(result, key)
		}
	}
	sort.Strings(result)
	return result
}

func idleConsumerState(environment, namespace, consumer string) domain.ConsumerState {
	return domain.ConsumerState{Environment: environment, Namespace: namespace, Consumer: consumer, DesiredRevision: 0, Status: "pending"}
}

func consumerStateAfterConfigurationLookup(environment, namespace, consumer string, configuration model.Configuration, err error) (domain.ConsumerState, error) {
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Missing namespace is an idle consumer, not a lookup failure.
			// 未写入的 namespace 对消费者是空闲状态，不是查询失败。
			return idleConsumerState(environment, namespace, consumer), nil
		}
		return domain.ConsumerState{}, fmt.Errorf("get consumer desired configuration: %w", err)
	}
	return domain.ConsumerState{Environment: environment, Namespace: namespace, Consumer: consumer, DesiredRevision: configuration.Revision, Status: "pending"}, nil
}

func decodeHeaders(encoded []byte) map[string]string {
	var headers map[string]string
	if len(encoded) == 0 || jsoncodec.Unmarshal(encoded, &headers) != nil {
		return nil
	}
	return headers
}

func snapshotFromIdempotency(prior model.ConfigIdempotency) (domain.Snapshot, error) {
	var public map[string]string
	if err := jsoncodec.Unmarshal(prior.ResponsePublicValues, &public); err != nil {
		return domain.Snapshot{}, fmt.Errorf("decode idempotent configuration response: %w", err)
	}
	var secretKeys []string
	if err := jsoncodec.Unmarshal(prior.ResponseSecretKeys, &secretKeys); err != nil {
		return domain.Snapshot{}, fmt.Errorf("decode idempotent configuration secret keys: %w", err)
	}
	secrets := make(map[string]string, len(secretKeys))
	for _, key := range secretKeys {
		secrets[key] = "configured"
	}
	return domain.Snapshot{Environment: prior.Environment, Namespace: prior.Namespace, Revision: prior.Revision, SchemaVersion: prior.SchemaVersion, Public: public, Secrets: secrets, UpdatedBy: prior.ActorID, CreatedAt: prior.CreatedAt, UpdatedAt: prior.ResponseUpdatedAt}, nil
}
