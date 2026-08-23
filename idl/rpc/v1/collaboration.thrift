namespace go collaboration
namespace rs collaboration

include "common.thrift"
include "knowledge.thrift"

const i32 CodeInvalidInput = 40001
const i32 CodeUnauthenticated = 40002
const i32 CodeForbidden = 40003
const i32 CodeNotFound = 40004
const i32 CodeConflict = 40005
const i32 CodePreconditionFailed = 40006
const i32 CodeUnavailable = 40007
const i32 CodeInternal = 40999

struct CreateSessionRequest {
  1: required string document_id
}

struct CollaborationSession {
  1: required string ticket
  2: required string subprotocol
  3: required string fragment
  4: required string access
  5: required string ticket_expires_at
  6: required string session_expires_at
  7: optional i32 instance_ordinal
}

struct Version {
  1: required string id
  2: required string document_id
  3: required i64 sequence
  4: required string kind
  5: optional string label
  6: required knowledge.PublicUser created_by
  7: required string created_at
}

struct PageInfo {
  1: optional string next_cursor
  2: required bool has_more
}

struct VersionPage {
  1: required list<Version> items
  2: required PageInfo page
}

struct VersionDetail {
  1: required Version version
  2: required knowledge.RichTextDocument content
  3: required string plain_text
}

struct ListVersionsRequest {
  1: required string document_id
  2: optional string cursor
  3: optional i32 limit
}

struct CreateVersionRequest {
  1: required string document_id
  2: optional string label
  3: optional string idempotency_key
  4: optional binary state_vector
}

struct GetVersionRequest {
  1: required string document_id
  2: required string version_id
}

struct RestoreVersionRequest {
  1: required string document_id
  2: required string version_id
  3: required i64 expected_sequence
  4: optional string idempotency_key
}

struct PurgeDocumentRequest {
  1: required string document_id
}

service CollaborationService {
  common.PingResponse Ping(1: common.PingRequest request)
  CollaborationSession CreateSession(1: CreateSessionRequest request)
  VersionPage ListVersions(1: ListVersionsRequest request)
  Version CreateVersion(1: CreateVersionRequest request)
  VersionDetail GetVersion(1: GetVersionRequest request)
  Version RestoreVersion(1: RestoreVersionRequest request)
  void PurgeDocument(1: PurgeDocumentRequest request)
}
