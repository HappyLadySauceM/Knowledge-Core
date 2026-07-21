package v1

import "time"

type CategoryResponse struct {
	ID            int64              `json:"id"`
	Name          string             `json:"name"`
	Slug          string             `json:"slug"`
	Path          string             `json:"path"`
	ParentID      int64              `json:"parent_id"`
	Sort          int                `json:"sort"`
	DocumentCount int64              `json:"document_count"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
	Children      []CategoryResponse `json:"children,omitempty"`
}

type TagResponse struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	Slug          string    `json:"slug"`
	DocumentCount int64     `json:"document_count"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type DocumentCategoryResponse struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
	Path string `json:"path"`
}

type DocumentTagResponse struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type DocumentResponse struct {
	ID             int64                     `json:"id"`
	Slug           string                    `json:"slug"`
	Title          string                    `json:"title"`
	Summary        string                    `json:"summary"`
	Content        string                    `json:"content,omitempty"`
	Blocks         []DocumentBlockResponse   `json:"blocks,omitempty"`
	CategoryID     int64                     `json:"category_id"`
	Category       *DocumentCategoryResponse `json:"category,omitempty"`
	Tags           []DocumentTagResponse     `json:"tags"`
	Source         string                    `json:"source"`
	Status         string                    `json:"status"`
	Confidence     float64                   `json:"confidence"`
	WordCount      int                       `json:"word_count"`
	CoverURL       string                    `json:"cover_url"`
	AuthorID       int64                     `json:"author_id"`
	CurrentVersion int64                     `json:"current_version"`
	CreatedAt      time.Time                 `json:"created_at"`
	UpdatedAt      time.Time                 `json:"updated_at"`
	PublishedAt    *time.Time                `json:"published_at,omitempty"`
}

type DocumentBlockResponse struct {
	BlockID     string    `json:"block_id"`
	ParentID    string    `json:"parent_id"`
	PositionKey string    `json:"position_key"`
	Type        string    `json:"type"`
	ContentJSON string    `json:"content_json"`
	TextContent string    `json:"text_content"`
	Version     int64     `json:"version"`
	UpdatedBy   int64     `json:"updated_by"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type ListDocumentsRequest struct {
	Page     int    `form:"page"`
	PageSize int    `form:"page_size"`
	Q        string `form:"q"`
	Category string `form:"category"`
	Tag      string `form:"tag"`
	Status   string `form:"status"`
}

type ListDocumentsResponse struct {
	Items    []DocumentResponse `json:"items"`
	Total    int64              `json:"total"`
	Page     int                `json:"page"`
	PageSize int                `json:"page_size"`
}

type CreateDocumentRequest struct {
	Slug       string               `json:"slug"`
	Title      string               `json:"title"`
	Summary    string               `json:"summary"`
	Content    string               `json:"content"`
	CategoryID int64                `json:"category_id"`
	TagIDs     []int64              `json:"tag_ids"`
	Source     string               `json:"source"`
	Status     string               `json:"status"`
	Confidence float64              `json:"confidence"`
	CoverURL   string               `json:"cover_url"`
	Blocks     []DocumentBlockInput `json:"blocks"`
}

type UpdateDocumentRequest struct {
	ExpectedVersion *int64                `json:"expected_version"`
	Slug            *string               `json:"slug"`
	Title           *string               `json:"title"`
	Summary         *string               `json:"summary"`
	Content         *string               `json:"content"`
	CategoryID      *int64                `json:"category_id"`
	TagIDs          *[]int64              `json:"tag_ids"`
	Source          *string               `json:"source"`
	Status          *string               `json:"status"`
	Confidence      *float64              `json:"confidence"`
	CoverURL        *string               `json:"cover_url"`
	Blocks          *[]DocumentBlockInput `json:"blocks"`
}

type DocumentBlockInput struct {
	BlockID     string `json:"block_id"`
	ParentID    string `json:"parent_id"`
	PositionKey string `json:"position_key"`
	Type        string `json:"type"`
	ContentJSON string `json:"content_json"`
	TextContent string `json:"text_content"`
}

type ApplyDocumentOpsRequest struct {
	Ops []DocumentOperationRequest `json:"ops"`
}

type DocumentOperationRequest struct {
	OpID                 string `json:"op_id"`
	BaseDocumentVersion  int64  `json:"base_document_version"`
	BlockID              string `json:"block_id"`
	ExpectedBlockVersion int64  `json:"expected_block_version"`
	Type                 string `json:"type"`
	PayloadJSON          string `json:"payload_json"`
}

type ApplyDocumentOpsResponse struct {
	Acks           []DocumentOperationAckResponse      `json:"acks"`
	Conflicts      []DocumentOperationConflictResponse `json:"conflicts,omitempty"`
	Document       DocumentResponse                    `json:"document"`
	CurrentVersion int64                               `json:"current_version"`
}

type DocumentOperationAckResponse struct {
	OpID            string `json:"op_id"`
	DocumentID      int64  `json:"document_id"`
	DocumentVersion int64  `json:"document_version"`
	BlockID         string `json:"block_id"`
	BlockVersion    int64  `json:"block_version"`
}

type DocumentOperationConflictResponse struct {
	OpID            string                `json:"op_id"`
	DocumentID      int64                 `json:"document_id"`
	DocumentVersion int64                 `json:"document_version"`
	Block           DocumentBlockResponse `json:"block"`
}

type ListCategoriesResponse struct {
	Items []CategoryResponse `json:"items"`
}

type CreateCategoryRequest struct {
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	ParentID *int64 `json:"parent_id"`
	Sort     *int   `json:"sort"`
}

type UpdateCategoryRequest struct {
	Name     *string `json:"name"`
	Slug     *string `json:"slug"`
	ParentID *int64  `json:"parent_id"`
	Sort     *int    `json:"sort"`
}

type ListTagsResponse struct {
	Items []TagResponse `json:"items"`
}

type CreateTagRequest struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type UpdateTagRequest struct {
	Name *string `json:"name"`
	Slug *string `json:"slug"`
}
