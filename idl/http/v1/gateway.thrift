namespace go gateway

struct HealthRequest {}

struct EmptyData {}

struct ErrorResponse {
  1: required i32 code (api.body="code")
  2: required string message (api.body="message")
  3: required EmptyData data (api.body="data")
  4: required string request_id (api.body="request_id")
}

struct HealthData {
  1: required string status (api.body="status")
  2: required string service (api.body="service")
}

struct HealthResponse {
  1: required i32 code (api.body="code")
  2: required string message (api.body="message")
  3: required HealthData data (api.body="data")
  4: required string request_id (api.body="request_id")
}

struct UserData {
  1: required i64 id (api.body="id")
  2: required string username (api.body="username")
  3: required string email (api.body="email")
  4: required string role (api.body="role")
  5: required string status (api.body="status")
  6: required string avatar (api.body="avatar")
  7: required string bio (api.body="bio")
  8: required i64 created_at_unix (api.body="created_at_unix")
  9: required i64 updated_at_unix (api.body="updated_at_unix")
}

struct RegisterRequest {
  1: required string username (api.body="username")
  2: required string email (api.body="email")
  3: required string password (api.body="password")
}

struct RegisterResponse {
  1: required i32 code (api.body="code")
  2: required string message (api.body="message")
  3: required UserData data (api.body="data")
  4: required string request_id (api.body="request_id")
}

struct LoginRequest {
  1: required string identifier (api.body="identifier")
  2: required string password (api.body="password")
}

struct LoginData {
  1: required UserData user (api.body="user")
  2: required string access_token (api.body="access_token")
  3: required string token_type (api.body="token_type")
  4: required i64 expires_at_unix (api.body="expires_at_unix")
}

struct LoginResponse {
  1: required i32 code (api.body="code")
  2: required string message (api.body="message")
  3: required LoginData data (api.body="data")
  4: required string request_id (api.body="request_id")
}

struct CurrentUserRequest {}

struct CurrentUserResponse {
  1: required i32 code (api.body="code")
  2: required string message (api.body="message")
  3: required UserData data (api.body="data")
  4: required string request_id (api.body="request_id")
}

struct DocumentData {
  1: required i64 id (api.body="id")
  2: required string title (api.body="title")
  3: required string summary (api.body="summary")
  4: required string slug (api.body="slug")
  5: required string status (api.body="status")
  6: required i64 author_id (api.body="author_id")
  7: required i64 current_version (api.body="current_version")
  8: optional i64 published_at_unix (api.body="published_at_unix")
  9: required i64 created_at_unix (api.body="created_at_unix")
  10: required i64 updated_at_unix (api.body="updated_at_unix")
}

struct DocumentBlockData {
  1: required string block_id (api.body="block_id")
  2: required i64 document_id (api.body="document_id")
  3: required string position_key (api.body="position_key")
  4: required string type (api.body="type")
  5: required string content_json (api.body="content_json")
  6: required string text_content (api.body="text_content")
  7: required i64 version (api.body="version")
  8: required i64 updated_by (api.body="updated_by")
  9: required i64 updated_at_unix (api.body="updated_at_unix")
}

struct DocumentDetailData {
  1: required DocumentData document (api.body="document")
  2: required list<DocumentBlockData> blocks (api.body="blocks")
}

struct DocumentListData {
  1: required list<DocumentData> items (api.body="items")
  2: required i64 total (api.body="total")
  3: required i32 page (api.body="page")
  4: required i32 page_size (api.body="page_size")
}

struct DocumentListRequest {
  1: optional string query (api.query="query")
  2: optional i32 page (api.query="page")
  3: optional i32 page_size (api.query="page_size")
}

struct DocumentIDRequest {
  1: required i64 document_id (api.path="document_id")
}

struct CreateDocumentRequest {
  1: required string title (api.body="title")
  2: optional string summary (api.body="summary")
}

struct UpdateDocumentRequest {
  1: required i64 document_id (api.path="document_id")
  2: required string title (api.body="title")
  3: required string summary (api.body="summary")
}

struct SetDocumentStatusRequest {
  1: required i64 document_id (api.path="document_id")
  2: required string status (api.body="status")
}

struct ApplyDocumentOperationRequest {
  1: required i64 document_id (api.path="document_id")
  2: required string op_id (api.body="op_id")
  3: required i64 base_document_version (api.body="base_document_version")
  4: required string block_id (api.body="block_id")
  5: required i64 base_block_version (api.body="base_block_version")
  6: required string position_key (api.body="position_key")
  7: required string content_json (api.body="content_json")
  8: required string text_content (api.body="text_content")
}

struct DocumentOperationAckData {
  1: required i64 document_id (api.body="document_id")
  2: required string op_id (api.body="op_id")
  3: required i64 document_version (api.body="document_version")
  4: required i64 block_version (api.body="block_version")
  5: required bool duplicate (api.body="duplicate")
}

struct DocumentResponse {
  1: required i32 code (api.body="code")
  2: required string message (api.body="message")
  3: required DocumentData data (api.body="data")
  4: required string request_id (api.body="request_id")
}

struct DocumentDetailResponse {
  1: required i32 code (api.body="code")
  2: required string message (api.body="message")
  3: required DocumentDetailData data (api.body="data")
  4: required string request_id (api.body="request_id")
}

struct DocumentListResponse {
  1: required i32 code (api.body="code")
  2: required string message (api.body="message")
  3: required DocumentListData data (api.body="data")
  4: required string request_id (api.body="request_id")
}

struct DocumentOperationAckResponse {
  1: required i32 code (api.body="code")
  2: required string message (api.body="message")
  3: required DocumentOperationAckData data (api.body="data")
  4: required string request_id (api.body="request_id")
}

service GatewayService {
  HealthResponse Live(1: HealthRequest request) (api.get="/health/live")
  HealthResponse Ready(1: HealthRequest request) (api.get="/health/ready")
  RegisterResponse Register(1: RegisterRequest request) (api.post="/api/v1/auth/register")
  LoginResponse Login(1: LoginRequest request) (api.post="/api/v1/auth/login")
  CurrentUserResponse CurrentUser(1: CurrentUserRequest request) (api.get="/api/v1/users/me")
  DocumentListResponse ListPublishedDocuments(1: DocumentListRequest request) (api.get="/api/v1/documents")
  DocumentDetailResponse GetPublishedDocument(1: DocumentIDRequest request) (api.get="/api/v1/documents/:document_id")
  DocumentListResponse ListDocuments(1: DocumentListRequest request) (api.get="/api/v1/studio/documents")
  DocumentDetailResponse CreateDocument(1: CreateDocumentRequest request) (api.post="/api/v1/studio/documents")
  DocumentDetailResponse GetDocument(1: DocumentIDRequest request) (api.get="/api/v1/studio/documents/:document_id")
  DocumentDetailResponse UpdateDocument(1: UpdateDocumentRequest request) (api.patch="/api/v1/studio/documents/:document_id")
  DocumentResponse DeleteDocument(1: DocumentIDRequest request) (api.delete="/api/v1/studio/documents/:document_id")
  DocumentResponse SetDocumentStatus(1: SetDocumentStatusRequest request) (api.patch="/api/v1/studio/documents/:document_id/status")
  DocumentOperationAckResponse ApplyDocumentOperation(1: ApplyDocumentOperationRequest request) (api.post="/api/v1/studio/documents/:document_id/ops")
}
