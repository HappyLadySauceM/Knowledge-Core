namespace go knowledge
namespace rs knowledge

include "common.thrift"

const i32 CodeInvalidInput = 30001
const i32 CodeNotFound = 30002
const i32 CodeConflict = 30003
const i32 CodeForbidden = 30004
const i32 CodeUnauthenticated = 30005
const i32 CodeUnavailable = 30006
const i32 CodePreconditionFailed = 30007
const i32 CodeGone = 30008
const i32 CodeQuotaExceeded = 30009
const i32 CodeInternal = 30999

struct PublicUser {
  1: required i64 id
  2: required string username
  3: required string avatar
}

struct RichTextAttrs {
  1: optional i32 level
  2: optional i32 start
  3: optional bool checked
  4: optional string language
  5: optional string href
  6: optional string attachment_id
  7: optional string alt
  8: optional string title
  9: optional string text_align
  10: optional i32 colspan
  11: optional i32 rowspan
  12: optional list<i32> colwidth
}

struct RichTextMark {
  1: required string type
  2: optional RichTextAttrs attrs
}

struct RichTextNode {
  1: required string type
  2: optional RichTextAttrs attrs
  3: optional list<RichTextNode> content
  4: optional string text
  5: optional list<RichTextMark> marks
}

struct RichTextDocument {
  1: required string type
  2: required list<RichTextNode> content
}

struct Document {
  1: required string id
  2: required string title
  3: required string summary
  4: required string slug
  5: required PublicUser owner
  6: required string access
  7: required bool published
  8: required i64 metadata_revision
  9: required i64 content_revision
  10: optional string published_at
  11: optional string deleted_at
  12: optional string projected_at
  13: required string created_at
  14: required string updated_at
}

struct Attachment {
  1: required string id
  2: required string document_id
  3: required string filename
  4: required string media_type
  5: required i64 size_bytes
  6: required string status
  7: required string created_at
}

struct DocumentDetail {
  1: required Document document
  2: required RichTextDocument content
  3: required string plain_text
  4: required list<Attachment> attachments
}

struct PageInfo {
  1: optional string next_cursor
  2: required bool has_more
}

struct DocumentPage {
  1: required list<Document> items
  2: required PageInfo page
}

struct EmptyRequest {}

struct ListDocumentsRequest {
  1: optional string query
  2: optional string cursor
  3: optional i32 limit
  4: optional string access
  5: optional string publication
}

struct GetPublishedDocumentRequest { 1: required string slug }
struct DocumentIDRequest { 1: required string document_id }

struct CreateDocumentRequest {
  1: required string title
  2: optional string summary
  3: optional string slug
  4: optional string idempotency_key
}

struct UpdateDocumentRequest {
  1: required string document_id
  2: required i64 expected_revision
  3: optional string title
  4: optional string summary
  5: optional string slug
}

struct SetPublicationRequest {
  1: required string document_id
  2: required i64 expected_revision
  3: required bool published
}

struct DeleteDocumentRequest {
  1: required string document_id
  2: required i64 expected_revision
}

struct Member {
  1: required PublicUser user
  2: required string role
  3: required i64 revision
  4: required string created_at
  5: required string updated_at
}

struct MemberList { 1: required list<Member> items }

struct AddMemberRequest {
  1: required string document_id
  2: required string username
  3: required string role
  4: optional string idempotency_key
}

struct UpdateMemberRequest {
  1: required string document_id
  2: required i64 user_id
  3: required i64 expected_revision
  4: required string role
}

struct DeleteMemberRequest {
  1: required string document_id
  2: required i64 user_id
  3: required i64 expected_revision
}

struct CreateAttachmentRequest {
  1: required string document_id
  2: required string filename
  3: required string media_type
  4: required i64 size_bytes
  5: required string sha256
  6: optional string idempotency_key
}

struct AttachmentUpload {
  1: required Attachment attachment
  2: required string upload_url
  3: required map<string,string> required_headers
  4: required string expires_at
}

struct AttachmentIDRequest {
  1: required string document_id
  2: required string attachment_id
}

struct AttachmentList { 1: required list<Attachment> items }

struct AttachmentContentRequest { 1: required string attachment_id }

struct AttachmentContent {
  1: required string url
  2: required string expires_at
}

struct AuthorizeCollaborationRequest {
  1: required string document_id
}

struct CollaborationAuthorization {
  1: required string document_id
  2: required PublicUser actor
  3: required string access
  4: required i64 permission_revision
  5: required string token_expires_at
}

struct ProjectCollaborationRequest {
  1: required string document_id
  2: required i64 sequence
  3: required RichTextDocument content
  4: required string plain_text
}

service KnowledgeService {
  common.PingResponse Ping(1: common.PingRequest request)
  common.PingResponse Live(1: common.PingRequest request)
  DocumentPage ListPublishedDocuments(1: ListDocumentsRequest request)
  DocumentDetail GetPublishedDocument(1: GetPublishedDocumentRequest request)
  DocumentPage ListDocuments(1: ListDocumentsRequest request)
  Document CreateDocument(1: CreateDocumentRequest request)
  Document GetDocument(1: DocumentIDRequest request)
  Document UpdateDocument(1: UpdateDocumentRequest request)
  Document SetPublication(1: SetPublicationRequest request)
  Document DeleteDocument(1: DeleteDocumentRequest request)
  Document RestoreDeletedDocument(1: DocumentIDRequest request)
  DocumentPage ListDeletedDocuments(1: ListDocumentsRequest request)
  MemberList ListMembers(1: DocumentIDRequest request)
  Member AddMember(1: AddMemberRequest request)
  Member UpdateMember(1: UpdateMemberRequest request)
  void DeleteMember(1: DeleteMemberRequest request)
  AttachmentList ListAttachments(1: DocumentIDRequest request)
  AttachmentUpload CreateAttachment(1: CreateAttachmentRequest request)
  Attachment CompleteAttachment(1: AttachmentIDRequest request)
  void DeleteAttachment(1: AttachmentIDRequest request)
  AttachmentContent GetAttachmentContent(1: AttachmentContentRequest request)
  CollaborationAuthorization AuthorizeCollaboration(1: AuthorizeCollaborationRequest request)
  void ProjectCollaboration(1: ProjectCollaborationRequest request)
}
