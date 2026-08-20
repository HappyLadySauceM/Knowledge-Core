namespace go identity

include "common.thrift"

const i32 CodeInvalidInput = 20001
const i32 CodeConflict = 20002
const i32 CodeInvalidCredentials = 20003
const i32 CodeAccountLocked = 20004
const i32 CodeUserDisabled = 20005
const i32 CodeUserNotFound = 20006
const i32 CodeUnauthenticated = 20007
const i32 CodeForbidden = 20008
const i32 CodeEmailNotVerified = 20010
const i32 CodeInternal = 20999

struct User {
  1: required i64 id
  2: required string username
  3: required string email
  4: required string role
  5: required string status
  6: required i64 token_version
  7: required string avatar
  8: required string bio
  9: required string created_at
  10: required string updated_at
}

struct PublicUser {
  1: required i64 id
  2: required string username
  3: required string avatar
}

struct RegisterRequest {
  1: required string username
  2: required string email
  3: required string password
}

struct AuthenticateRequest {
  1: required string identifier
  2: required string password
}

struct Authentication {
  1: required User user
  2: required string access_token
  3: required string expires_at
  4: optional string refresh_token
  5: optional string session_id
  6: optional string token_type
}

struct RefreshSessionRequest {
  1: required string refresh_token
}

struct EmailTokenRequest { 1: required string token }
struct EmailRequest { 1: required string email }
struct PasswordResetRequest {
  1: required string token
  2: required string password
}
struct PasswordResetRequestRequest { 1: required string identifier }
struct SessionRequest { 1: required string session_id }
struct DeactivateAccountRequest { 1: required string password }

struct Session {
  1: required string id
  2: required string device_label
  3: required string created_at
  4: required string last_seen_at
  5: required string expires_at
  6: required bool current
}
struct SessionList { 1: required list<Session> items }

struct CurrentUserRequest {}

struct ResolveUserRequest {
  1: required string username
}

service IdentityService {
  common.PingResponse Ping(1: common.PingRequest request)
  User Register(1: RegisterRequest request)
  Authentication Authenticate(1: AuthenticateRequest request)
  Authentication RefreshSession(1: RefreshSessionRequest request)
  common.EmptyResponse RequestEmailVerification(1: EmailRequest request)
  common.EmptyResponse VerifyEmail(1: EmailTokenRequest request)
  common.EmptyResponse RequestPasswordReset(1: PasswordResetRequestRequest request)
  common.EmptyResponse ResetPassword(1: PasswordResetRequest request)
  SessionList ListSessions(1: CurrentUserRequest request)
  common.EmptyResponse RevokeSession(1: SessionRequest request)
  common.EmptyResponse RevokeAllSessions(1: CurrentUserRequest request)
  common.EmptyResponse DeactivateAccount(1: DeactivateAccountRequest request)
  User GetCurrentUser(1: CurrentUserRequest request)
  PublicUser ResolveUser(1: ResolveUserRequest request)
}
