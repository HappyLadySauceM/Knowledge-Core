package document

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode"

	apperrors "github.com/HappyLadySauce/Knowledge-Core/internal/errors"
	"github.com/HappyLadySauce/Knowledge-Core/internal/taxonomy"
	"github.com/HappyLadySauce/Knowledge-Core/internal/user"
)

type Service struct {
	repo       *Repository
	taxonomies *taxonomy.Repository
}

func NewService(db *sql.DB) (*Service, error) {
	return &Service{
		repo:       NewRepository(db),
		taxonomies: taxonomy.NewRepository(db),
	}, nil
}

func (s *Service) ListPublic(ctx context.Context, query ListQuery) (ListResult, error) {
	query.Status = StatusPublished
	return s.repo.List(ctx, query)
}

func (s *Service) GetPublic(ctx context.Context, id int64) (Detail, error) {
	item, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	return s.publicDetail(ctx, item)
}

func (s *Service) GetPublicBySlug(ctx context.Context, slug string) (Detail, error) {
	if strings.TrimSpace(slug) == "" {
		return Detail{}, apperrors.InvalidRequest
	}
	item, err := s.repo.GetBySlug(ctx, slug)
	if err != nil {
		return Detail{}, err
	}
	return s.publicDetail(ctx, item)
}

func (s *Service) publicDetail(ctx context.Context, item Document) (Detail, error) {
	if item.Status != StatusPublished {
		return Detail{}, apperrors.NotFound
	}
	content, blocks, err := s.repo.GetPublishedRevision(ctx, item.ID)
	if err != nil {
		return Detail{}, err
	}
	return Detail{Document: item, Content: content, Blocks: blocks}, nil
}

func (s *Service) ListAdmin(ctx context.Context, actor user.User, query ListQuery) (ListResult, error) {
	if actor.Role != user.RoleAdmin {
		return ListResult{}, apperrors.Forbidden
	}
	if query.Status != "" && !validStatus(query.Status) {
		return ListResult{}, apperrors.InvalidRequest
	}
	return s.repo.List(ctx, query)
}

func (s *Service) CreateAdmin(ctx context.Context, actor user.User, cmd CreateCommand) (Detail, error) {
	if actor.Role != user.RoleAdmin {
		return Detail{}, apperrors.Forbidden
	}
	normalized, _, _, blocks, content, err := s.normalizeCreate(ctx, actor.ID, cmd)
	if err != nil {
		return Detail{}, err
	}

	exists, err := s.repo.SlugExists(ctx, normalized.Slug, 0)
	if err != nil {
		return Detail{}, err
	}
	if exists {
		return Detail{}, apperrors.Conflict
	}
	created, err := s.repo.Create(ctx, normalized, blocks, content)
	if err != nil {
		return Detail{}, err
	}
	createdBlocks, err := s.repo.GetBlocks(ctx, created.ID)
	if err != nil {
		return Detail{}, err
	}
	return Detail{Document: created, Content: blocksToMarkdown(createdBlocks), Blocks: createdBlocks}, nil
}

func (s *Service) GetAdmin(ctx context.Context, actor user.User, id int64) (Detail, error) {
	if actor.Role != user.RoleAdmin {
		return Detail{}, apperrors.Forbidden
	}
	return s.detail(ctx, id)
}

func (s *Service) UpdateAdmin(ctx context.Context, actor user.User, id int64, cmd UpdateCommand) (Detail, error) {
	if actor.Role != user.RoleAdmin {
		return Detail{}, apperrors.Forbidden
	}
	if id <= 0 {
		return Detail{}, apperrors.InvalidRequest
	}
	current, err := s.detail(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	if cmd.ExpectedVersion != nil && *cmd.ExpectedVersion != current.CurrentVersion {
		return Detail{}, apperrors.Conflict
	}
	next, _, _, blocks, content, err := s.normalizeUpdate(ctx, current, cmd)
	if err != nil {
		return Detail{}, err
	}

	if next.Slug != current.Slug {
		exists, err := s.repo.SlugExists(ctx, next.Slug, id)
		if err != nil {
			return Detail{}, err
		}
		if exists {
			return Detail{}, apperrors.Conflict
		}
	}
	updated, err := s.repo.Update(ctx, id, next, blocks, content, cmd.ExpectedVersion)
	if err != nil {
		return Detail{}, err
	}
	updatedBlocks, err := s.repo.GetBlocks(ctx, updated.ID)
	if err != nil {
		return Detail{}, err
	}
	return Detail{Document: updated, Content: blocksToMarkdown(updatedBlocks), Blocks: updatedBlocks}, nil
}

func (s *Service) DeleteAdmin(ctx context.Context, actor user.User, id int64) error {
	if actor.Role != user.RoleAdmin {
		return apperrors.Forbidden
	}
	if id <= 0 {
		return apperrors.InvalidRequest
	}
	if _, err := s.repo.GetByID(ctx, id); err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	return nil
}

func (s *Service) ApplyOpsAdmin(ctx context.Context, actor user.User, id int64, cmd ApplyOpsCommand) (ApplyOpsResult, error) {
	if actor.Role != user.RoleAdmin {
		return ApplyOpsResult{}, apperrors.Forbidden
	}
	if id <= 0 || len(cmd.Ops) == 0 {
		return ApplyOpsResult{}, apperrors.InvalidRequest
	}
	result, err := s.repo.ApplyOps(ctx, id, actor.ID, cmd.Ops)
	if err != nil {
		return ApplyOpsResult{}, err
	}
	if len(result.Conflicts) > 0 {
		return result, apperrors.Conflict
	}
	return result, nil
}

func (s *Service) detail(ctx context.Context, id int64) (Detail, error) {
	if id <= 0 {
		return Detail{}, apperrors.InvalidRequest
	}
	item, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	blocks, err := s.repo.GetBlocks(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	return Detail{Document: item, Content: blocksToMarkdown(blocks), Blocks: blocks}, nil
}

func (s *Service) normalizeCreate(ctx context.Context, actorID int64, cmd CreateCommand) (record, string, []string, []Block, string, error) {
	title := strings.TrimSpace(cmd.Title)
	content := strings.TrimSpace(cmd.Content)
	if len(cmd.Blocks) > 0 {
		content = blocksToMarkdown(blockInputsToBlocks(cmd.Blocks, actorID, time.Now().UTC()))
	}
	if title == "" || content == "" {
		return record{}, "", nil, nil, "", apperrors.InvalidRequest
	}
	slug := normalizeSlug(cmd.Slug, title)
	if slug == "" {
		return record{}, "", nil, nil, "", apperrors.InvalidRequest
	}
	status := normalizeStatus(cmd.Status)
	source := normalizeSource(cmd.Source)
	if status == "" || source == "" {
		return record{}, "", nil, nil, "", apperrors.InvalidRequest
	}
	category, categoryPath, err := s.resolveCategory(ctx, cmd.CategoryID)
	if err != nil {
		return record{}, "", nil, nil, "", err
	}
	tags, err := s.resolveTags(ctx, cmd.TagIDs)
	if err != nil {
		return record{}, "", nil, nil, "", err
	}
	tagIDs, tagNames := tagIDsAndNames(tags)
	now := time.Now().UTC()
	blocks := markdownToBlocks(content, actorID, now)
	if len(cmd.Blocks) > 0 {
		blocks = blockInputsToBlocks(cmd.Blocks, actorID, now)
		content = blocksToMarkdown(blocks)
	}
	publishedAt := publishedAtFor("", status, nil, now)
	doc := Document{
		Slug:           slug,
		Title:          title,
		Summary:        strings.TrimSpace(cmd.Summary),
		CategoryID:     category.ID,
		Source:         source,
		Status:         status,
		Confidence:     cmd.Confidence,
		WordCount:      countWords(content),
		CoverURL:       strings.TrimSpace(cmd.CoverURL),
		AuthorID:       actorID,
		CurrentVersion: 1,
		CreatedAt:      now,
		UpdatedAt:      now,
		PublishedAt:    publishedAt,
	}
	return record{Document: doc, SearchText: buildSearchText(doc, categoryPath, tagNames, content), TagIDs: tagIDs}, categoryPath, tagNames, blocks, content, nil
}

func (s *Service) normalizeUpdate(ctx context.Context, current Detail, cmd UpdateCommand) (record, string, []string, []Block, string, error) {
	doc := current.Document
	content := current.Content
	if cmd.Title != nil {
		doc.Title = strings.TrimSpace(*cmd.Title)
	}
	if cmd.Slug != nil {
		doc.Slug = normalizeSlug(*cmd.Slug, doc.Title)
	}
	if cmd.Summary != nil {
		doc.Summary = strings.TrimSpace(*cmd.Summary)
	}
	if cmd.Content != nil {
		content = strings.TrimSpace(*cmd.Content)
	}
	if cmd.Source != nil {
		doc.Source = normalizeSource(*cmd.Source)
	}
	if cmd.Status != nil {
		doc.Status = normalizeStatus(*cmd.Status)
	}
	if cmd.Confidence != nil {
		doc.Confidence = *cmd.Confidence
	}
	if cmd.CoverURL != nil {
		doc.CoverURL = strings.TrimSpace(*cmd.CoverURL)
	}
	if doc.Title == "" || doc.Slug == "" || content == "" || doc.Source == "" || doc.Status == "" {
		return record{}, "", nil, nil, "", apperrors.InvalidRequest
	}

	nextCategoryID := doc.CategoryID
	if cmd.CategoryID != nil {
		nextCategoryID = *cmd.CategoryID
	}
	category, categoryPath, err := s.resolveCategory(ctx, nextCategoryID)
	if err != nil {
		return record{}, "", nil, nil, "", err
	}
	doc.CategoryID = category.ID

	tagIDs := make([]int64, 0, len(doc.Tags))
	for _, tag := range doc.Tags {
		tagIDs = append(tagIDs, tag.ID)
	}
	if cmd.TagIDs != nil {
		tagIDs = *cmd.TagIDs
	}
	tags, err := s.resolveTags(ctx, tagIDs)
	if err != nil {
		return record{}, "", nil, nil, "", err
	}
	nextTagIDs, tagNames := tagIDsAndNames(tags)
	now := time.Now().UTC()
	blocks := current.Blocks
	if cmd.Content != nil {
		blocks = markdownToBlocks(content, doc.AuthorID, now)
	}
	if cmd.Blocks != nil {
		blocks = blockInputsToBlocks(*cmd.Blocks, doc.AuthorID, now)
		content = blocksToMarkdown(blocks)
	}
	if len(blocks) == 0 {
		return record{}, "", nil, nil, "", apperrors.InvalidRequest
	}
	doc.WordCount = countWords(content)
	doc.UpdatedAt = now
	doc.PublishedAt = publishedAtFor(current.Status, doc.Status, current.PublishedAt, now)
	doc.CurrentVersion = current.CurrentVersion + 1
	return record{Document: doc, SearchText: buildSearchText(doc, categoryPath, tagNames, content), TagIDs: nextTagIDs}, categoryPath, tagNames, blocks, content, nil
}

func (s *Service) resolveCategory(ctx context.Context, id int64) (CategorySummary, string, error) {
	if id == 0 {
		return CategorySummary{}, "", nil
	}
	if id < 0 {
		return CategorySummary{}, "", apperrors.InvalidRequest
	}
	category, err := s.taxonomies.GetCategoryByID(ctx, id)
	if err != nil {
		return CategorySummary{}, "", err
	}
	return CategorySummary{ID: category.ID, Name: category.Name, Slug: category.Slug, Path: category.Path}, category.Path, nil
}

func (s *Service) resolveTags(ctx context.Context, ids []int64) ([]taxonomy.Tag, error) {
	ids = dedupeIDs(ids)
	for _, id := range ids {
		if id <= 0 {
			return nil, apperrors.InvalidRequest
		}
	}
	tags, err := s.taxonomies.ListTagsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	if len(tags) != len(ids) {
		return nil, apperrors.InvalidRequest
	}
	return tags, nil
}

func normalizeSlug(slug, title string) string {
	if strings.TrimSpace(slug) == "" {
		slug = taxonomy.SlugFromName(title)
		if slug == "" {
			slug = "document"
		}
		return slug
	}
	return taxonomy.NormalizeSlug(slug)
}

func normalizeStatus(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return StatusDraft
	}
	if validStatus(status) {
		return status
	}
	return ""
}

func validStatus(status string) bool {
	return status == StatusDraft || status == StatusPublished
}

func normalizeSource(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return SourceManual
	}
	switch source {
	case SourceManual, SourceImport, SourceAgent:
		return source
	default:
		return ""
	}
}

func publishedAtFor(previousStatus, nextStatus string, current *time.Time, now time.Time) *time.Time {
	if nextStatus != StatusPublished {
		return nil
	}
	if previousStatus == StatusPublished && current != nil {
		return current
	}
	published := now
	return &published
}

func buildSearchText(doc Document, categoryPath string, tagNames []string, content string) string {
	parts := []string{doc.Title, doc.Summary, categoryPath, strings.Join(tagNames, " "), content}
	return strings.ToLower(strings.Join(parts, "\n"))
}

func buildSearchTextFromBlocks(doc Document, blocks []Block) string {
	return buildSearchText(doc, "", nil, blocksToMarkdown(blocks))
}

func countWords(content string) int {
	fields := strings.Fields(content)
	if len(fields) > 1 {
		return len(fields)
	}
	count := 0
	for _, r := range content {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		count++
	}
	return count
}

func tagIDsAndNames(tags []taxonomy.Tag) ([]int64, []string) {
	ids := make([]int64, 0, len(tags))
	names := make([]string, 0, len(tags))
	for _, tag := range tags {
		ids = append(ids, tag.ID)
		names = append(names, tag.Name)
	}
	return ids, names
}

func dedupeIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func (d Detail) String() string {
	return fmt.Sprintf("document id=%d slug=%s status=%s", d.ID, d.Slug, d.Status)
}
