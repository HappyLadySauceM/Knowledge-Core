namespace go gateway

struct HealthRequest {}

struct EmptyData {}

struct ErrorResponse {
  1: required i32 code (api.body="code")
  2: required string message (api.body="message")
  3: required EmptyData data (api.body="data")
  4: required string request_id (api.body="request_id")
}

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

struct UserData {
  1: required i64 id (api.body="id")
  2: required string username (api.body="username")
  3: required string email (api.body="email")
  4: required string role (api.body="role")
  5: required string status (api.body="status")
  6: required string avatar (api.body="avatar")
  7: required string bio (api.body="bio")
  8: required i64 created_at_unix (api.body="created_at_unix")
  9: required i64 updated_at_unix (api.body="updated_at_unix")
}

struct RegisterRequest {
  1: required string username (api.body="username")
  2: required string email (api.body="email")
  3: required string password (api.body="password")
}

struct RegisterResponse {
  1: required i32 code (api.body="code")
  2: required string message (api.body="message")
  3: required UserData data (api.body="data")
  4: required string request_id (api.body="request_id")
}

struct LoginRequest {
  1: required string identifier (api.body="identifier")
  2: required string password (api.body="password")
}

struct LoginData {
  1: required UserData user (api.body="user")
  2: required string access_token (api.body="access_token")
  3: required string token_type (api.body="token_type")
  4: required i64 expires_at_unix (api.body="expires_at_unix")
}

struct LoginResponse {
  1: required i32 code (api.body="code")
  2: required string message (api.body="message")
  3: required LoginData data (api.body="data")
  4: required string request_id (api.body="request_id")
}

struct CurrentUserRequest {}

struct CurrentUserResponse {
  1: required i32 code (api.body="code")
  2: required string message (api.body="message")
  3: required UserData data (api.body="data")
  4: required string request_id (api.body="request_id")
}

service GatewayService {
  HealthResponse Live(1: HealthRequest request) (api.get="/health/live")
  HealthResponse Ready(1: HealthRequest request) (api.get="/health/ready")
  RegisterResponse Register(1: RegisterRequest request) (api.post="/api/v1/auth/register")
  LoginResponse Login(1: LoginRequest request) (api.post="/api/v1/auth/login")
  CurrentUserResponse CurrentUser(1: CurrentUserRequest request) (api.get="/api/v1/users/me")
}
