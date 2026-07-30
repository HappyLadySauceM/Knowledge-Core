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
  9: required i64 created_at_unix
  10: required i64 updated_at_unix
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
  3: required i64 expires_at_unix
}

struct GetUserRequest {
  1: required i64 user_id
}

service IdentityService {
  common.PingResponse Ping(1: common.PingRequest request)
  User Register(1: RegisterRequest request)
  Authentication Authenticate(1: AuthenticateRequest request)
  User GetUser(1: GetUserRequest request)
}
