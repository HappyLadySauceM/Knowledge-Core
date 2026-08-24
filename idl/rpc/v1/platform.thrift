namespace go platform

include "common.thrift"

const i32 CodeInvalidInput = 32001
const i32 CodeNotFound = 32002
const i32 CodeConflict = 32003
const i32 CodeForbidden = 32004
const i32 CodeUnauthenticated = 32005
const i32 CodeUnavailable = 32006
const i32 CodePreconditionFailed = 32007
const i32 CodeInternal = 32999

struct ConfigValue {
  1: required string key
  2: required string value
  3: required bool secret
  4: required bool redacted
}

struct Configuration {
  1: required string environment
  2: required string namespace
  3: required i64 revision
  4: required i32 schema_version
  5: required list<ConfigValue> values
  6: required string updated_at
  7: required i64 updated_by
}

struct GetConfigurationRequest {
  1: required string namespace
}

struct PutConfigurationRequest {
  1: required string namespace
  2: required i64 expected_revision
  3: required string idempotency_key
  4: required map<string, string> values
}

struct SiteProfile {
  1: required string title
  2: required string tagline_zh
  3: required string tagline_en
  4: required string hero_image_url
  5: required double hero_focal_x
  6: required double hero_focal_y
  7: required i64 revision
  8: optional string hero_attachment_id
}

struct GetConfigurationDeliveryRequest {
  1: required string namespace
  2: required i64 revision
}

struct GetConsumerConfigurationRequest {
  1: required string namespace
  2: required i64 revision
  3: required string consumer
}

struct GetConsumerStateRequest {
  1: required string namespace
  2: required string consumer
}

struct ConsumerConfigurationState {
  1: required string environment
  2: required string namespace
  3: required string consumer
  4: required i64 desired_revision
  5: required i64 applied_revision
  6: required string status
  7: optional string last_error_key
}

struct ReportConfigurationApplyRequest {
  1: required string message_id
  2: required string namespace
  3: required i64 revision
  4: required string consumer
  5: required string status
  6: required i32 attempts
  7: optional string last_error_key
}

struct ConfigurationDelivery {
  1: required string message_id
  2: required string namespace
  3: required i64 revision
  4: required string status
  5: required i32 attempts
  6: optional string last_error_key
  7: optional string published_at
}

service PlatformService {
  common.PingResponse Ping(1: common.PingRequest request)
  common.PingResponse Live(1: common.PingRequest request)
  SiteProfile GetSiteProfile(1: common.EmptyResponse request)
  Configuration GetConfiguration(1: GetConfigurationRequest request)
  Configuration PutConfiguration(1: PutConfigurationRequest request)
  ConfigurationDelivery GetConfigurationDelivery(1: GetConfigurationDeliveryRequest request)
  Configuration GetConsumerConfiguration(1: GetConsumerConfigurationRequest request)
  ConsumerConfigurationState GetConsumerState(1: GetConsumerStateRequest request)
  common.EmptyResponse ReportConfigurationApply(1: ReportConfigurationApplyRequest request)
}
