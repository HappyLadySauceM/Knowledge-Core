namespace go attachment
namespace rs attachment

include "common.thrift"

const i32 CodeInvalidInput = 31001
const i32 CodeNotFound = 31002
const i32 CodeConflict = 31003
const i32 CodeForbidden = 31004
const i32 CodeUnauthenticated = 31005
const i32 CodeUnavailable = 31006
const i32 CodePreconditionFailed = 31007
const i32 CodeGone = 31008
const i32 CodeQuotaExceeded = 31009
const i32 CodeInternal = 31999

struct Attachment {
  1: required string id
  2: required i64 owner_id
  3: required string filename
  4: required string media_type
  5: required string category
  6: required i64 size_bytes
  7: required string sha256
  8: required string status
  9: required i32 part_size
  10: required i32 part_count
  11: required string created_at
  12: optional string detected_type
  13: optional string download_url
  14: optional string download_expires_at
}

struct UploadPart {
  1: required i32 part_number
  2: required string url
  3: required string expires_at
}

struct AttachmentUpload {
  1: required Attachment attachment
  2: required string upload_id
  3: required list<UploadPart> parts
  4: required string expires_at
}

struct CreateAttachmentRequest {
  1: required string filename
  2: required string media_type
  3: required i64 size_bytes
  4: optional string idempotency_key
}

struct CompletePart {
  1: required i32 part_number
  2: required string etag
}

struct CompleteAttachmentRequest {
  1: required string attachment_id
  2: required string upload_id
  3: required list<CompletePart> parts
}

struct AttachmentIDRequest { 1: required string attachment_id }
struct AttachmentList { 1: required list<Attachment> items }
struct ListAttachmentsRequest {
  1: optional string status
  2: optional string category
  3: optional string cursor
  4: optional i32 limit
}

service AttachmentService {
  common.PingResponse Ping(1: common.PingRequest request)
  common.PingResponse Live(1: common.PingRequest request)
  AttachmentUpload CreateAttachment(1: CreateAttachmentRequest request)
  Attachment CompleteAttachment(1: CompleteAttachmentRequest request)
  AttachmentList ListAttachments(1: ListAttachmentsRequest request)
  Attachment GetAttachment(1: AttachmentIDRequest request)
  void TrashAttachment(1: AttachmentIDRequest request)
  Attachment RestoreAttachment(1: AttachmentIDRequest request)
}
