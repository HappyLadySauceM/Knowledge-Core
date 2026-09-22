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
  15: optional string language
  16: optional list<string> tags
  17: optional string folder_id
  18: required string publication_status
  19: optional string publication_error
  20: optional string publication_hash
  21: optional string icon
  22: optional string cover_attachment_id
  23: optional double cover_focal_x
  24: optional double cover_focal_y
}

struct DocumentDetail {
  1: required Document document
  2: required RichTextDocument content
  3: required string plain_text
}

struct PageInfo {
  1: optional string next_cursor
  2: required bool has_more
}

struct DocumentPage {
  1: required list<Document> items
  2: required PageInfo page
}

struct Folder {
  1: required string id
  2: optional string parent_id
  3: required string name
  4: required i32 depth
  5: required i64 revision
  6: required string created_at
  7: required string updated_at
}

struct FolderList { 1: required list<Folder> items }

struct EmptyRequest {}

struct ListDocumentsRequest {
  1: optional string query
  2: optional string cursor
  3: optional i32 limit
  4: optional string access
  5: optional string publication
  6: optional string folder_id
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
  6: optional string language
  7: optional list<string> tags
  8: optional string folder_id
  9: optional string icon
  10: optional string cover_attachment_id
  11: optional double cover_focal_x
  12: optional double cover_focal_y
}

struct PublishSnapshotRequest {
  1: required string document_id
  2: required i64 expected_metadata_revision
  3: required string title
  4: required string summary
  5: required string slug
  6: required string language
  7: required list<string> tags
  8: required RichTextDocument content
  9: required string plain_text
  10: optional string idempotency_key
  11: optional string publication_hash
  12: optional string icon
  13: optional string cover_attachment_id
  14: optional double cover_focal_x
  15: optional double cover_focal_y
}

enum CommitKind {
  MANUAL = 1,
  LEAVE = 2,
  SAFETY = 3,
  PUBLISH = 4,
  REVISION_MERGE = 5,
  AGENT_EDIT = 6,
  RESTORE = 7,
}

struct Commit {
  1: required string id
  2: required string document_id
  3: required CommitKind kind
  4: required string label
  5: optional string description
  6: required string contributor
  7: required i64 sequence
  8: required string content_hash
  9: required RichTextDocument content
  10: required string plain_text
  11: required string created_at
  12: required string updated_at
}

struct CommitPage {
  1: required list<Commit> items
  2: required PageInfo page
}

struct CreateCommitRequest {
  1: required string document_id
  2: required CommitKind kind
  3: optional string label
  4: optional string description
  5: optional string content_hash
  6: optional RichTextDocument content
  7: optional string plain_text
  8: optional string idempotency_key
}

struct ListCommitsRequest {
  1: required string document_id
  2: optional i32 limit
  3: optional string cursor
}

struct CommitIDRequest { 1: required string commit_id }

struct RenameCommitRequest {
  1: required string commit_id
  2: required string label
  3: optional string description
}

struct RestoreCommitRequest {
  1: required string commit_id
  2: optional string idempotency_key
}

struct ListFoldersRequest { 1: optional string parent_id }
struct CreateFolderRequest {
  1: required string name
  2: optional string parent_id
  3: optional string idempotency_key
}
struct UpdateFolderRequest {
  1: required string folder_id
  2: required i64 expected_revision
  3: optional string name
  4: optional string parent_id
}
struct DeleteFolderRequest {
  1: required string folder_id
  2: required i64 expected_revision
}

struct SetPublicationRequest {
  1: required string document_id
  2: required i64 expected_revision
  3: required bool published
  4: optional string idempotency_key
}

struct DeleteDocumentRequest {
  1: required string document_id
  2: required i64 expected_revision
}

struct PurgeDeletedDocumentRequest {
  1: required string document_id
  2: required i64 expected_revision
  3: optional string idempotency_key
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

struct PublishedMediaRequest { 1: required string attachment_id }
struct PublishedMediaAuthorization { 1: required bool published }

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
  Document PublishSnapshot(1: PublishSnapshotRequest request)
  FolderList ListFolders(1: ListFoldersRequest request)
  Folder CreateFolder(1: CreateFolderRequest request)
  Folder UpdateFolder(1: UpdateFolderRequest request)
  void DeleteFolder(1: DeleteFolderRequest request)
  Document SetPublication(1: SetPublicationRequest request)
  Document DeleteDocument(1: DeleteDocumentRequest request)
  void PurgeDeletedDocument(1: PurgeDeletedDocumentRequest request)
  Document RestoreDeletedDocument(1: DocumentIDRequest request)
  DocumentPage ListDeletedDocuments(1: ListDocumentsRequest request)
  MemberList ListMembers(1: DocumentIDRequest request)
  Member AddMember(1: AddMemberRequest request)
  Member UpdateMember(1: UpdateMemberRequest request)
  void DeleteMember(1: DeleteMemberRequest request)
  PublishedMediaAuthorization IsMediaPublished(1: PublishedMediaRequest request)
  CollaborationAuthorization AuthorizeCollaboration(1: AuthorizeCollaborationRequest request)
  void ProjectCollaboration(1: ProjectCollaborationRequest request)
  Commit CreateCommit(1: CreateCommitRequest request)
  CommitPage ListCommits(1: ListCommitsRequest request)
  Commit GetCommit(1: CommitIDRequest request)
  Commit RenameCommit(1: RenameCommitRequest request)
  Document RestoreCommit(1: RestoreCommitRequest request)
}
