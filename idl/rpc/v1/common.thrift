namespace go common
namespace rs common

struct PingRequest {
  1: optional string message
}

struct PingResponse {
  1: required string service
  2: required string status
  3: required i64 unix_time
}

struct EmptyResponse {}
