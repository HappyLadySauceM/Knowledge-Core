namespace go knowledge

include "common.thrift"

const i32 CodeInvalidInput = 30001
const i32 CodeNotFound = 30002
const i32 CodeConflict = 30003
const i32 CodeForbidden = 30004
const i32 CodeInternal = 30999

struct Document {
  1: required i64 id
  2: required string title
  3: required string summary
  4: required string slug
  5: required string status
  6: required i64 author_id
  7: required i64 current_version
  8: optional i64 published_at_unix
  9: required i64 created_at_unix
  10: required i64 updated_at_unix
}

struct DocumentBlock {
  1: required string block_id
  2: required i64 document_id
  3: required string position_key
  4: required string type
  5: required string content_json
  6: required string text_content
  7: required i64 version
  8: required i64 updated_by
  9: required i64 updated_at_unix
}

struct DocumentDetail {
  1: required Document document
  2: required list<DocumentBlock> blocks
}

struct DocumentListRequest {
  1: optional string query
  2: optional i32 page
  3: optional i32 page_size
}

struct DocumentList {
  1: required list<Document> items
  2: required i64 total
  3: required i32 page
  4: required i32 page_size
}

struct DocumentIDRequest {
  1: required i64 document_id
}

struct CreateDocumentRequest {
  1: required string title
  2: optional string summary
}

struct UpdateDocumentRequest {
  1: required i64 document_id
  2: required string title
  3: required string summary
}

struct SetDocumentStatusRequest {
  1: required i64 document_id
  2: required string status
}

struct ApplyDocumentOperationRequest {
  1: required i64 document_id
  2: required string op_id
  3: required i64 base_document_version
  4: required string block_id
  5: required i64 base_block_version
  6: required string position_key
  7: required string content_json
  8: required string text_content
}

struct DocumentOperationAck {
  1: required i64 document_id
  2: required string op_id
  3: required i64 document_version
  4: required i64 block_version
  5: required bool duplicate
}

service KnowledgeService {
  common.PingResponse Ping(1: common.PingRequest request)
  DocumentList ListPublishedDocuments(1: DocumentListRequest request)
  DocumentList ListDocuments(1: DocumentListRequest request)
  DocumentDetail GetPublishedDocument(1: DocumentIDRequest request)
  DocumentDetail CreateDocument(1: CreateDocumentRequest request)
  DocumentDetail GetDocument(1: DocumentIDRequest request)
  DocumentDetail UpdateDocument(1: UpdateDocumentRequest request)
  Document DeleteDocument(1: DocumentIDRequest request)
  Document SetDocumentStatus(1: SetDocumentStatusRequest request)
  DocumentOperationAck ApplyDocumentOperation(1: ApplyDocumentOperationRequest request)
}
