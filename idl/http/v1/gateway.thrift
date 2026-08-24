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
struct SiteProfileData {
  1: required string title (api.body="title")
  2: required string tagline_zh (api.body="tagline_zh")
  3: required string tagline_en (api.body="tagline_en")
  4: required string hero_image_url (api.body="hero_image_url")
  5: required double hero_focal_x (api.body="hero_focal_x")
  6: required double hero_focal_y (api.body="hero_focal_y")
  7: required i64 revision (api.body="revision")
  8: optional string hero_attachment_id (api.body="hero_attachment_id")
}

struct ConfigurationValueData {
  1: required string key (api.body="key")
  2: required string value (api.body="value")
  3: required bool secret (api.body="secret")
  4: required bool redacted (api.body="redacted")
}

struct ConfigurationData {
  1: required string environment (api.body="environment")
  2: required string namespace (api.body="namespace")
  3: required i64 revision (api.body="revision")
  4: required i32 schema_version (api.body="schema_version")
  5: required list<ConfigurationValueData> values (api.body="values")
  6: required string updated_at (api.body="updated_at")
  7: required string updated_by (api.body="updated_by")
}

struct GetConfigurationRequest {
  1: required string namespace (api.path="namespace")
}

struct PutConfigurationRequest {
  1: required string namespace (api.path="namespace")
  2: required string if_match (api.header="If-Match")
  3: required string idempotency_key (api.header="Idempotency-Key")
  4: required map<string, string> values (api.body="values")
}

struct GetConfigurationDeliveryRequest {
  1: required string namespace (api.path="namespace")
  2: required i64 revision (api.path="revision")
}

struct ConfigurationDeliveryData {
  1: required string message_id (api.body="message_id")
  2: required string namespace (api.body="namespace")
  3: required i64 revision (api.body="revision")
  4: required string status (api.body="status")
  5: required i32 attempts (api.body="attempts")
  6: optional string last_error_key (api.body="last_error_key")
  7: optional string published_at (api.body="published_at")
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
  10: optional string avatar_attachment_id (api.body="avatar_attachment_id")
}

struct PublicUserData {
  1: required string id (api.body="id")
  2: required string username (api.body="username")
  3: required string avatar (api.body="avatar")
  4: optional string avatar_attachment_id (api.body="avatar_attachment_id")
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
  5: optional string refresh_token (api.body="refresh_token")
  6: optional string session_id (api.body="session_id")
}

struct RefreshSessionRequest {
  1: required string refresh_token (api.body="refresh_token")
}

struct EmailTokenRequest { 1: required string token (api.body="token") }
struct EmailRequest { 1: required string email (api.body="email") }
struct PasswordResetRequest {
  1: required string token (api.body="token")
  2: required string password (api.body="password")
}
struct PasswordResetRequestRequest { 1: required string identifier (api.body="identifier") }
struct SessionRequest { 1: required string session_id (api.path="session_id") }
struct DeactivateAccountRequest { 1: required string password (api.body="password") }
struct SessionDataItem {
  1: required string id (api.body="id")
  2: required string device_label (api.body="device_label")
  3: required string created_at (api.body="created_at")
  4: required string last_seen_at (api.body="last_seen_at")
  5: required string expires_at (api.body="expires_at")
  6: required bool current (api.body="current")
}
struct SessionListData { 1: required list<SessionDataItem> items (api.body="items") }

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
  15: optional string language (api.body="language")
  16: optional list<string> tags (api.body="tags")
  17: optional string folder_id (api.body="folder_id")
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
}

struct CollaborationSessionData {
  1: required string websocket_url (api.body="websocket_url")
  2: required string ticket (api.body="ticket")
  3: required string subprotocol (api.body="subprotocol")
  4: required string fragment (api.body="fragment")
  5: required string access (api.body="access")
  6: required string ticket_expires_at (api.body="ticket_expires_at")
  7: required string session_expires_at (api.body="session_expires_at")
}

struct PageInfoData {
  1: optional string next_cursor (api.body="next_cursor")
  2: required bool has_more (api.body="has_more")
}

struct DocumentPageData {
  1: required list<DocumentData> items (api.body="items")
  2: required PageInfoData page (api.body="page")
}

struct FolderData {
  1: required string id (api.body="id")
  2: optional string parent_id (api.body="parent_id")
  3: required string name (api.body="name")
  4: required i32 depth (api.body="depth")
  5: required i64 revision (api.body="revision")
  6: required string created_at (api.body="created_at")
  7: required string updated_at (api.body="updated_at")
}
struct FolderListData { 1: required list<FolderData> items (api.body="items") }

struct ListDocumentsRequest {
  1: optional string query (api.query="q")
  2: optional string cursor (api.query="cursor")
  3: optional i32 limit (api.query="limit")
  4: optional string access (api.query="access")
  5: optional string publication (api.query="publication")
}

struct ListFoldersRequest { 1: optional string parent_id (api.query="parent_id") }
struct CreateFolderRequest {
  1: required string name (api.body="name")
  2: optional string parent_id (api.body="parent_id")
  3: optional string idempotency_key (api.header="Idempotency-Key")
}
struct UpdateFolderRequest {
  1: required string folder_id (api.path="folder_id")
  2: required string if_match (api.header="If-Match")
  3: optional string name (api.body="name")
  4: optional string parent_id (api.body="parent_id")
}
struct DeleteFolderRequest {
  1: required string folder_id (api.path="folder_id")
  2: required string if_match (api.header="If-Match")
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
  6: optional string language (api.body="language")
  7: optional list<string> tags (api.body="tags")
  8: optional string folder_id (api.body="folder_id")
}

struct PublicationRequest {
  1: required string document_id (api.path="document_id")
  2: required string if_match (api.header="If-Match")
  3: optional string state_vector (api.body="state_vector")
  4: optional string idempotency_key (api.header="Idempotency-Key")
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

// Attachment service façade. These types are deliberately separate from the
// legacy document-scoped attachment projection above so clients can migrate
// without mixing document ownership with the generic media library.
struct MediaAttachmentData {
  1: required string id (api.body="id")
  2: required i64 owner_id (api.body="owner_id")
  3: required string filename (api.body="filename")
  4: required string media_type (api.body="media_type")
  5: required string category (api.body="category")
  6: required i64 size_bytes (api.body="size_bytes")
  7: required string sha256 (api.body="sha256")
  8: required string status (api.body="status")
  9: required i32 part_size (api.body="part_size")
  10: required i32 part_count (api.body="part_count")
  11: required string created_at (api.body="created_at")
  12: optional string detected_type (api.body="detected_type")
  13: required string content_url (api.body="content_url")
}
struct MediaAttachmentPartData {
  1: required i32 part_number (api.body="part_number")
  2: required string url (api.body="url")
  3: required string expires_at (api.body="expires_at")
}
struct MediaAttachmentUploadData {
  1: required MediaAttachmentData attachment (api.body="attachment")
  2: required string upload_id (api.body="upload_id")
  3: required list<MediaAttachmentPartData> parts (api.body="parts")
  4: required string expires_at (api.body="expires_at")
}
struct CreateMediaAttachmentRequest {
  1: required string filename (api.body="filename")
  2: required string media_type (api.body="media_type")
  3: required i64 size_bytes (api.body="size_bytes")
  4: optional string idempotency_key (api.header="Idempotency-Key")
}
struct CompleteMediaAttachmentPart {
  1: required i32 part_number (api.body="part_number")
  2: required string etag (api.body="etag")
}
struct CompleteMediaAttachmentRequest {
  1: required string attachment_id (api.path="attachment_id")
  2: required string upload_id (api.body="upload_id")
  3: required list<CompleteMediaAttachmentPart> parts (api.body="parts")
}
struct MediaAttachmentPathRequest { 1: required string attachment_id (api.path="attachment_id") }
struct ListMediaAttachmentsRequest {
  1: optional string status (api.query="status")
  2: optional string category (api.query="category")
  3: optional string cursor (api.query="cursor")
  4: optional i32 limit (api.query="limit")
}
struct MediaAttachmentListData { 1: required list<MediaAttachmentData> items (api.body="items") }

service GatewayService {
  HealthData Live(1: EmptyRequest request) (api.get="/health/live")
  HealthData Ready(1: EmptyRequest request) (api.get="/health/ready")
  UserData Register(1: RegisterRequest request) (api.post="/api/v1/users")
  SessionData Login(1: LoginRequest request) (api.post="/api/v1/sessions")
  SessionData RefreshSession(1: RefreshSessionRequest request) (api.post="/api/v1/sessions/refresh")
  EmptyResponse Logout(1: EmptyRequest request) (api.delete="/api/v1/sessions/current")
  SessionListData ListSessions(1: EmptyRequest request) (api.get="/api/v1/sessions")
  EmptyResponse RevokeSession(1: SessionRequest request) (api.delete="/api/v1/sessions/:session_id")
  EmptyResponse RevokeAllSessions(1: EmptyRequest request) (api.delete="/api/v1/sessions")
  EmptyResponse RequestEmailVerification(1: EmailRequest request) (api.post="/api/v1/email-verification-requests")
  EmptyResponse VerifyEmail(1: EmailTokenRequest request) (api.post="/api/v1/email-verifications")
  EmptyResponse RequestPasswordReset(1: PasswordResetRequestRequest request) (api.post="/api/v1/password-reset-requests")
  EmptyResponse ResetPassword(1: PasswordResetRequest request) (api.post="/api/v1/password-resets")
  EmptyResponse DeactivateAccount(1: DeactivateAccountRequest request) (api.post="/api/v1/users/me/deactivation")
  UserData CurrentUser(1: EmptyRequest request) (api.get="/api/v1/users/me")
  DocumentPageData ListPublishedDocuments(1: ListDocumentsRequest request) (api.get="/api/v1/documents")
  DocumentDetailData GetPublishedDocument(1: SlugRequest request) (api.get="/api/v1/documents/:slug")
  SiteProfileData GetSiteProfile(1: EmptyRequest request) (api.get="/api/v1/site-profile")
  ConfigurationData GetConfiguration(1: GetConfigurationRequest request) (api.get="/api/v1/admin/configuration/:namespace")
  ConfigurationData PutConfiguration(1: PutConfigurationRequest request) (api.put="/api/v1/admin/configuration/:namespace")
  ConfigurationDeliveryData GetConfigurationDelivery(1: GetConfigurationDeliveryRequest request) (api.get="/api/v1/admin/configuration/:namespace/deliveries/:revision")
  EmptyResponse GetAttachmentContent(1: PublicAttachmentRequest request) (api.get="/api/v1/attachments/:attachment_id/content")
  MediaAttachmentListData ListMediaAttachments(1: ListMediaAttachmentsRequest request) (api.get="/api/v1/attachments")
  MediaAttachmentUploadData CreateMediaAttachment(1: CreateMediaAttachmentRequest request) (api.post="/api/v1/attachments")
  MediaAttachmentData GetMediaAttachment(1: MediaAttachmentPathRequest request) (api.get="/api/v1/attachments/:attachment_id")
  MediaAttachmentData CompleteMediaAttachment(1: CompleteMediaAttachmentRequest request) (api.post="/api/v1/attachments/:attachment_id/complete")
  EmptyResponse DeleteMediaAttachment(1: MediaAttachmentPathRequest request) (api.delete="/api/v1/attachments/:attachment_id")
  MediaAttachmentData RestoreMediaAttachment(1: MediaAttachmentPathRequest request) (api.post="/api/v1/attachments/:attachment_id/restore")
  DocumentPageData ListDocuments(1: ListDocumentsRequest request) (api.get="/api/v1/studio/documents")
  FolderListData ListFolders(1: ListFoldersRequest request) (api.get="/api/v1/studio/folders")
  FolderData CreateFolder(1: CreateFolderRequest request) (api.post="/api/v1/studio/folders")
  FolderData UpdateFolder(1: UpdateFolderRequest request) (api.patch="/api/v1/studio/folders/:folder_id")
  EmptyResponse DeleteFolder(1: DeleteFolderRequest request) (api.delete="/api/v1/studio/folders/:folder_id")
  DocumentData CreateDocument(1: CreateDocumentRequest request) (api.post="/api/v1/studio/documents")
  DocumentData GetDocument(1: DocumentIDRequest request) (api.get="/api/v1/studio/documents/:document_id")
  CollaborationSessionData CreateCollaborationSession(1: DocumentIDRequest request) (api.post="/api/v1/studio/documents/:document_id/collaboration-sessions")
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
