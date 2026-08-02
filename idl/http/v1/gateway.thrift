namespace go gateway

const i32 CodeNotReady = 10001
const i32 CodeInvalidRequest = 10002
const i32 CodeAuthenticationRequired = 10003
const i32 CodePermissionDenied = 10005
const i32 CodeDependencyUnavailable = 10007
const i32 CodeRouteNotFound = 10008
const i32 CodeMethodNotAllowed = 10009
const i32 CodeRateLimited = 10010
const i32 CodeUpstreamTimeout = 10011
const i32 CodeInvalidUpstreamResponse = 10012
const i32 CodePreconditionFailed = 10013
const i32 CodeInternal = 10999

struct EmptyRequest {}
struct EmptyResponse {}

struct HealthData {
  1: required string status (api.body="status")
  2: required string service (api.body="service")
}

struct UserData {
  1: required string id (api.body="id")
  2: required string username (api.body="username")
  3: required string email (api.body="email")
  4: required string role (api.body="role")
  5: required string status (api.body="status")
  6: required string avatar (api.body="avatar")
  7: required string bio (api.body="bio")
  8: required string created_at (api.body="created_at")
  9: required string updated_at (api.body="updated_at")
}

struct PublicUserData {
  1: required string id (api.body="id")
  2: required string username (api.body="username")
  3: required string avatar (api.body="avatar")
}

struct RegisterRequest {
  1: required string username (api.body="username")
  2: required string email (api.body="email")
  3: required string password (api.body="password")
}

struct LoginRequest {
  1: required string identifier (api.body="identifier")
  2: required string password (api.body="password")
}

struct SessionData {
  1: required UserData user (api.body="user")
  2: required string access_token (api.body="access_token")
  3: required string token_type (api.body="token_type")
  4: required string expires_at (api.body="expires_at")
}

struct RichTextAttrsData {
  1: optional i32 level (api.body="level")
  2: optional i32 start (api.body="start")
  3: optional bool checked (api.body="checked")
  4: optional string language (api.body="language")
  5: optional string href (api.body="href")
  6: optional string attachment_id (api.body="attachmentId")
  7: optional string alt (api.body="alt")
  8: optional string title (api.body="title")
  9: optional string text_align (api.body="textAlign")
  10: optional i32 colspan (api.body="colspan")
  11: optional i32 rowspan (api.body="rowspan")
  12: optional list<i32> colwidth (api.body="colwidth")
}

struct RichTextMarkData {
  1: required string type (api.body="type")
  2: optional RichTextAttrsData attrs (api.body="attrs")
}

struct RichTextNodeData {
  1: required string type (api.body="type")
  2: optional RichTextAttrsData attrs (api.body="attrs")
  3: optional list<RichTextNodeData> content (api.body="content")
  4: optional string text (api.body="text")
  5: optional list<RichTextMarkData> marks (api.body="marks")
}

struct RichTextDocumentData {
  1: required string type (api.body="type")
  2: required list<RichTextNodeData> content (api.body="content")
}

struct DocumentData {
  1: required string id (api.body="id")
  2: required string title (api.body="title")
  3: required string summary (api.body="summary")
  4: required string slug (api.body="slug")
  5: required PublicUserData owner (api.body="owner")
  6: required string access (api.body="access")
  7: required bool published (api.body="published")
  8: required i64 metadata_revision (api.body="metadata_revision")
  9: required i64 content_revision (api.body="content_revision")
  10: optional string published_at (api.body="published_at")
  11: optional string deleted_at (api.body="deleted_at")
  12: optional string projected_at (api.body="projected_at")
  13: required string created_at (api.body="created_at")
  14: required string updated_at (api.body="updated_at")
}

struct AttachmentData {
  1: required string id (api.body="id")
  2: required string document_id (api.body="document_id")
  3: required string filename (api.body="filename")
  4: required string media_type (api.body="media_type")
  5: required i64 size_bytes (api.body="size_bytes")
  6: required string status (api.body="status")
  7: required string content_url (api.body="content_url")
  8: required string created_at (api.body="created_at")
}

struct DocumentDetailData {
  1: required DocumentData document (api.body="document")
  2: required RichTextDocumentData content (api.body="content")
  3: required string plain_text (api.body="plain_text")
  4: required list<AttachmentData> attachments (api.body="attachments")
  5: optional string websocket_url (api.body="websocket_url")
  6: optional string fragment (api.body="fragment")
}

struct PageInfoData {
  1: optional string next_cursor (api.body="next_cursor")
  2: required bool has_more (api.body="has_more")
}

struct DocumentPageData {
  1: required list<DocumentData> items (api.body="items")
  2: required PageInfoData page (api.body="page")
}

struct ListDocumentsRequest {
  1: optional string query (api.query="q")
  2: optional string cursor (api.query="cursor")
  3: optional i32 limit (api.query="limit")
  4: optional string access (api.query="access")
  5: optional string publication (api.query="publication")
}

struct SlugRequest { 1: required string slug (api.path="slug") }
struct DocumentIDRequest { 1: required string document_id (api.path="document_id") }

struct CreateDocumentRequest {
  1: required string title (api.body="title")
  2: optional string summary (api.body="summary")
  3: optional string slug (api.body="slug")
  4: optional string idempotency_key (api.header="Idempotency-Key")
}

struct UpdateDocumentRequest {
  1: required string document_id (api.path="document_id")
  2: required string if_match (api.header="If-Match")
  3: optional string title (api.body="title")
  4: optional string summary (api.body="summary")
  5: optional string slug (api.body="slug")
}

struct PublicationRequest {
  1: required string document_id (api.path="document_id")
  2: required string if_match (api.header="If-Match")
}

struct DeleteDocumentRequest {
  1: required string document_id (api.path="document_id")
  2: required string if_match (api.header="If-Match")
}

struct MemberData {
  1: required PublicUserData user (api.body="user")
  2: required string role (api.body="role")
  3: required i64 revision (api.body="revision")
  4: required string created_at (api.body="created_at")
  5: required string updated_at (api.body="updated_at")
}

struct MemberListData { 1: required list<MemberData> items (api.body="items") }

struct AddMemberRequest {
  1: required string document_id (api.path="document_id")
  2: required string username (api.body="username")
  3: required string role (api.body="role")
  4: optional string idempotency_key (api.header="Idempotency-Key")
}

struct MemberPathRequest {
  1: required string document_id (api.path="document_id")
  2: required string user_id (api.path="user_id")
  3: required string if_match (api.header="If-Match")
}

struct UpdateMemberRequest {
  1: required string document_id (api.path="document_id")
  2: required string user_id (api.path="user_id")
  3: required string if_match (api.header="If-Match")
  4: required string role (api.body="role")
}

struct VersionData {
  1: required string id (api.body="id")
  2: required string document_id (api.body="document_id")
  3: required i64 sequence (api.body="sequence")
  4: required string kind (api.body="kind")
  5: optional string label (api.body="label")
  6: required PublicUserData created_by (api.body="created_by")
  7: required string created_at (api.body="created_at")
}

struct VersionDetailData {
  1: required VersionData version (api.body="version")
  2: required RichTextDocumentData content (api.body="content")
  3: required string plain_text (api.body="plain_text")
}

struct VersionPageData {
  1: required list<VersionData> items (api.body="items")
  2: required PageInfoData page (api.body="page")
}

struct ListVersionsRequest {
  1: required string document_id (api.path="document_id")
  2: optional string cursor (api.query="cursor")
  3: optional i32 limit (api.query="limit")
}

struct CreateVersionRequest {
  1: required string document_id (api.path="document_id")
  2: optional string label (api.body="label")
  3: optional string idempotency_key (api.header="Idempotency-Key")
}

struct VersionPathRequest {
  1: required string document_id (api.path="document_id")
  2: required string version_id (api.path="version_id")
}

struct RestoreVersionRequest {
  1: required string document_id (api.path="document_id")
  2: required string version_id (api.path="version_id")
  3: required i64 expected_sequence (api.body="expected_sequence")
  4: optional string idempotency_key (api.header="Idempotency-Key")
}

struct CreateAttachmentRequest {
  1: required string document_id (api.path="document_id")
  2: required string filename (api.body="filename")
  3: required string media_type (api.body="media_type")
  4: required i64 size_bytes (api.body="size_bytes")
  5: required string sha256 (api.body="sha256")
  6: optional string idempotency_key (api.header="Idempotency-Key")
}

struct AttachmentUploadData {
  1: required AttachmentData attachment (api.body="attachment")
  2: required string upload_url (api.body="upload_url")
  3: required map<string,string> required_headers (api.body="required_headers")
  4: required string expires_at (api.body="expires_at")
}

struct AttachmentPathRequest {
  1: required string document_id (api.path="document_id")
  2: required string attachment_id (api.path="attachment_id")
}

struct PublicAttachmentRequest { 1: required string attachment_id (api.path="attachment_id") }
struct AttachmentListData { 1: required list<AttachmentData> items (api.body="items") }

service GatewayService {
  HealthData Live(1: EmptyRequest request) (api.get="/health/live")
  HealthData Ready(1: EmptyRequest request) (api.get="/health/ready")
  UserData Register(1: RegisterRequest request) (api.post="/api/v1/users")
  SessionData Login(1: LoginRequest request) (api.post="/api/v1/sessions")
  UserData CurrentUser(1: EmptyRequest request) (api.get="/api/v1/users/me")
  DocumentPageData ListPublishedDocuments(1: ListDocumentsRequest request) (api.get="/api/v1/documents")
  DocumentDetailData GetPublishedDocument(1: SlugRequest request) (api.get="/api/v1/documents/:slug")
  EmptyResponse GetAttachmentContent(1: PublicAttachmentRequest request) (api.get="/api/v1/attachments/:attachment_id/content")
  DocumentPageData ListDocuments(1: ListDocumentsRequest request) (api.get="/api/v1/studio/documents")
  DocumentData CreateDocument(1: CreateDocumentRequest request) (api.post="/api/v1/studio/documents")
  DocumentData GetDocument(1: DocumentIDRequest request) (api.get="/api/v1/studio/documents/:document_id")
  DocumentData UpdateDocument(1: UpdateDocumentRequest request) (api.patch="/api/v1/studio/documents/:document_id")
  EmptyResponse DeleteDocument(1: DeleteDocumentRequest request) (api.delete="/api/v1/studio/documents/:document_id")
  DocumentData PublishDocument(1: PublicationRequest request) (api.put="/api/v1/studio/documents/:document_id/publication")
  EmptyResponse UnpublishDocument(1: PublicationRequest request) (api.delete="/api/v1/studio/documents/:document_id/publication")
  MemberListData ListMembers(1: DocumentIDRequest request) (api.get="/api/v1/studio/documents/:document_id/members")
  MemberData AddMember(1: AddMemberRequest request) (api.post="/api/v1/studio/documents/:document_id/members")
  MemberData UpdateMember(1: UpdateMemberRequest request) (api.patch="/api/v1/studio/documents/:document_id/members/:user_id")
  EmptyResponse DeleteMember(1: MemberPathRequest request) (api.delete="/api/v1/studio/documents/:document_id/members/:user_id")
  VersionPageData ListVersions(1: ListVersionsRequest request) (api.get="/api/v1/studio/documents/:document_id/versions")
  VersionData CreateVersion(1: CreateVersionRequest request) (api.post="/api/v1/studio/documents/:document_id/versions")
  VersionDetailData GetVersion(1: VersionPathRequest request) (api.get="/api/v1/studio/documents/:document_id/versions/:version_id")
  VersionData RestoreVersion(1: RestoreVersionRequest request) (api.post="/api/v1/studio/documents/:document_id/versions/:version_id/restorations")
  AttachmentListData ListAttachments(1: DocumentIDRequest request) (api.get="/api/v1/studio/documents/:document_id/attachments")
  AttachmentUploadData CreateAttachment(1: CreateAttachmentRequest request) (api.post="/api/v1/studio/documents/:document_id/attachments")
  AttachmentData CompleteAttachment(1: AttachmentPathRequest request) (api.post="/api/v1/studio/documents/:document_id/attachments/:attachment_id/complete")
  EmptyResponse DeleteAttachment(1: AttachmentPathRequest request) (api.delete="/api/v1/studio/documents/:document_id/attachments/:attachment_id")
  DocumentPageData ListDeletedDocuments(1: ListDocumentsRequest request) (api.get="/api/v1/studio/trash")
  DocumentData RestoreDeletedDocument(1: DocumentIDRequest request) (api.post="/api/v1/studio/trash/:document_id/restore")
}
