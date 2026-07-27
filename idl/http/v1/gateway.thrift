namespace go gateway

struct HealthRequest {}

struct HealthData {
  1: required string status (api.body="status")
  2: required string service (api.body="service")
}

struct HealthResponse {
  1: required i32 code (api.body="code")
  2: required string message (api.body="message")
  3: required HealthData data (api.body="data")
  4: required string request_id (api.body="request_id")
}

service GatewayService {
  HealthResponse Live(1: HealthRequest request) (api.get="/health/live")
  HealthResponse Ready(1: HealthRequest request) (api.get="/health/ready")
}
