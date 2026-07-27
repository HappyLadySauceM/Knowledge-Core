namespace go identity

include "common.thrift"

service IdentityService {
  common.PingResponse Ping(1: common.PingRequest request)
}
