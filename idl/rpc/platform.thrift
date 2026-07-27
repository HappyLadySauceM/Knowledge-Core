namespace go platform

include "common.thrift"

service PlatformService {
  common.PingResponse Ping(1: common.PingRequest request)
}
