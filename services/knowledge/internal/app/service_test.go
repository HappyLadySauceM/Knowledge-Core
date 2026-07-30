package app_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/internal/codec/json"
	"github.com/HappyLadySauce/Knowledge-Core/internal/database"
	knowledgeapp "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/app"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/repository"
)

func TestGetUsesRepeatableReadTransaction(t *testing.T) {
	db := &fakeDB{}
	documents := &fakeDocuments{
		document: &domain.Document{ID: 7, CurrentVersion: 3},
		blocks:   []*domain.Block{{BlockID: "block-1", DocumentID: 7, Version: 2}},
	}
	service, err := knowledgeapp.NewService(db, documents, jsoncodec.New())
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	detail, err := service.Get(context.Background(), 7)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if detail.Document.CurrentVersion != 3 || len(detail.Blocks) != 1 {
		t.Fatalf("Get() detail = %#v", detail)
	}
	if db.options == nil || db.options.Isolation != sql.LevelRepeatableRead || !db.options.ReadOnly {
		t.Fatalf("BeginTx() options = %#v", db.options)
	}
	if db.transaction.commits != 1 || db.transaction.rollbacks != 0 {
		t.Fatalf("transaction commits = %d, rollbacks = %d", db.transaction.commits, db.transaction.rollbacks)
	}
	if documents.findExecutor != db.transaction || documents.listExecutor != db.transaction {
		t.Fatal("Get() did not use the repeatable-read transaction for both reads")
	}
}

func TestApplyOperationLocksAndValidatesCompleteReplay(t *testing.T) {
	operation := domain.Operation{
		DocumentID:          7,
		OperationID:         "op-1",
		BaseDocumentVersion: 2,
		BlockID:             "block-1",
		BaseBlockVersion:    1,
		PositionKey:         "a",
		ContentJSON:         `{"text":"hello"}`,
		TextContent:         "hello",
		ActorID:             42,
	}
	want := domain.OperationAck{
		DocumentID:      7,
		OperationID:     "op-1",
		DocumentVersion: 3,
		BlockVersion:    2,
		Duplicate:       true,
	}

	t.Run("exact replay", func(t *testing.T) {
		db := &fakeDB{}
		documents := &fakeDocuments{stored: repository.StoredOperation{Operation: operation, Ack: want, Type: "upsert_block"}}
		service, _ := knowledgeapp.NewService(db, documents, jsoncodec.New())

		got, err := service.ApplyOperation(context.Background(), operation)
		if err != nil {
			t.Fatalf("ApplyOperation() error = %v", err)
		}
		if got != want {
			t.Fatalf("ApplyOperation() = %#v, want %#v", got, want)
		}
		if len(documents.calls) != 2 || documents.calls[0] != "lock" || documents.calls[1] != "find" {
			t.Fatalf("repository calls = %v", documents.calls)
		}
		if db.transaction.commits != 1 || db.transaction.rollbacks != 0 {
			t.Fatalf("transaction commits = %d, rollbacks = %d", db.transaction.commits, db.transaction.rollbacks)
		}
	})

	t.Run("mismatched replay", func(t *testing.T) {
		db := &fakeDB{}
		documents := &fakeDocuments{stored: repository.StoredOperation{Operation: operation, Ack: want, Type: "upsert_block"}}
		service, _ := knowledgeapp.NewService(db, documents, jsoncodec.New())
		changed := operation
		changed.TextContent = "changed"

		_, err := service.ApplyOperation(context.Background(), changed)
		if !errors.Is(err, knowledgeapp.ErrVersionConflict) {
			t.Fatalf("ApplyOperation() error = %v", err)
		}
		if len(documents.calls) != 2 || documents.calls[0] != "lock" || documents.calls[1] != "find" {
			t.Fatalf("repository calls = %v", documents.calls)
		}
		if db.transaction.commits != 0 || db.transaction.rollbacks != 1 {
			t.Fatalf("transaction commits = %d, rollbacks = %d", db.transaction.commits, db.transaction.rollbacks)
		}
	})
}

func TestTransactionRollsBackOnPanic(t *testing.T) {
	db := &fakeDB{}
	documents := &fakeDocuments{panicOnFind: true}
	service, err := knowledgeapp.NewService(db, documents, jsoncodec.New())
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_, _ = service.Get(context.Background(), 7)
	}()
	if recovered == nil {
		t.Fatal("Get() did not propagate the repository panic")
	}
	if db.transaction == nil {
		t.Fatal("Get() did not begin a transaction")
	}
	if db.transaction.rollbacks != 1 {
		t.Fatalf("transaction rollbacks = %d, want 1", db.transaction.rollbacks)
	}
}

type fakeDB struct {
	options     *sql.TxOptions
	transaction *fakeTx
}

func (f *fakeDB) BeginTx(_ context.Context, options *sql.TxOptions) (database.Tx, error) {
	f.options = options
	f.transaction = &fakeTx{}
	return f.transaction, nil
}

func (*fakeDB) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	panic("unexpected ExecContext call")
}

func (*fakeDB) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	panic("unexpected QueryContext call")
}

func (*fakeDB) QueryRowContext(context.Context, string, ...any) *sql.Row {
	panic("unexpected QueryRowContext call")
}

func (*fakeDB) PingContext(context.Context) error { return nil }
func (*fakeDB) Close() error                      { return nil }

type fakeTx struct {
	commits   int
	rollbacks int
}

func (*fakeTx) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	panic("unexpected ExecContext call")
}

func (*fakeTx) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	panic("unexpected QueryContext call")
}

func (*fakeTx) QueryRowContext(context.Context, string, ...any) *sql.Row {
	panic("unexpected QueryRowContext call")
}

func (f *fakeTx) Commit() error {
	f.commits++
	return nil
}

func (f *fakeTx) Rollback() error {
	if f.commits > 0 {
		return sql.ErrTxDone
	}
	f.rollbacks++
	return nil
}

type fakeDocuments struct {
	document     *domain.Document
	blocks       []*domain.Block
	stored       repository.StoredOperation
	findExecutor database.Executor
	listExecutor database.Executor
	calls        []string
	panicOnFind  bool
}

func (*fakeDocuments) Create(context.Context, database.Executor, *domain.Document) error { return nil }

func (f *fakeDocuments) FindByID(_ context.Context, executor database.Executor, _ int64) (*domain.Document, error) {
	f.findExecutor = executor
	if f.panicOnFind {
		panic("repository panic")
	}
	return f.document, nil
}

func (*fakeDocuments) FindByIDForUpdate(context.Context, database.Executor, int64) (*domain.Document, error) {
	return nil, errors.New("unexpected FindByIDForUpdate call")
}

func (*fakeDocuments) FindPublishedByID(context.Context, database.Executor, int64) (*repository.PublishedDocument, error) {
	return nil, errors.New("unexpected FindPublishedByID call")
}

func (*fakeDocuments) List(context.Context, database.Executor, string, int, int, bool) (domain.List, error) {
	return domain.List{}, errors.New("unexpected List call")
}

func (f *fakeDocuments) ListBlocks(_ context.Context, executor database.Executor, _ int64) ([]*domain.Block, error) {
	f.listExecutor = executor
	return f.blocks, nil
}

func (*fakeDocuments) FindBlock(context.Context, database.Executor, int64, string) (*domain.Block, error) {
	return nil, errors.New("unexpected FindBlock call")
}

func (*fakeDocuments) UpdateMetadata(context.Context, database.Executor, *domain.Document) error {
	return errors.New("unexpected UpdateMetadata call")
}

func (*fakeDocuments) Delete(context.Context, database.Executor, int64) error {
	return errors.New("unexpected Delete call")
}

func (*fakeDocuments) SetStatus(context.Context, database.Executor, *domain.Document, string, *int64) error {
	return errors.New("unexpected SetStatus call")
}

func (f *fakeDocuments) LockOperation(context.Context, database.Executor, string) error {
	f.calls = append(f.calls, "lock")
	return nil
}

func (f *fakeDocuments) FindOperation(context.Context, database.Executor, string) (repository.StoredOperation, error) {
	f.calls = append(f.calls, "find")
	return f.stored, nil
}

func (*fakeDocuments) SaveBlock(context.Context, database.Executor, *domain.Block) error {
	return errors.New("unexpected SaveBlock call")
}

func (*fakeDocuments) IncrementVersion(context.Context, database.Executor, *domain.Document) error {
	return errors.New("unexpected IncrementVersion call")
}

func (*fakeDocuments) SaveOperation(context.Context, database.Executor, domain.Operation, domain.OperationAck, []byte) error {
	return errors.New("unexpected SaveOperation call")
}

func (*fakeDocuments) SaveRevision(context.Context, database.Executor, *domain.Document, int64, []byte) (int64, error) {
	return 0, errors.New("unexpected SaveRevision call")
}

var _ database.DB = (*fakeDB)(nil)
var _ database.Tx = (*fakeTx)(nil)
var _ repository.DocumentRepository = (*fakeDocuments)(nil)
