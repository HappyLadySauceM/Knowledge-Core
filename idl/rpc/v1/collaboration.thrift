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
  // Deprecated compatibility field. WebSocket placement is handled by the
  // single /v1/documents/{document_id} route and this field is never set.
  7: optional i32 instance_ordinal
}

// Captures the current durable collaboration state for publication without
// creating a history record. The state vector is a lower-bound barrier: the
// returned projection contains every client clock known by the caller.
struct CapturePublicationSnapshotRequest {
  1: required string document_id
  2: required binary state_vector
}

struct PublicationSnapshot {
  1: required string document_id
  2: required i64 sequence
  3: required knowledge.RichTextDocument content
  4: required string plain_text
  5: required string content_hash
}

struct PurgeDocumentRequest {
  1: required string document_id
}

service CollaborationService {
  common.PingResponse Ping(1: common.PingRequest request)
  CollaborationSession CreateSession(1: CreateSessionRequest request)
  PublicationSnapshot CapturePublicationSnapshot(1: CapturePublicationSnapshotRequest request)
  void PurgeDocument(1: PurgeDocumentRequest request)
}
