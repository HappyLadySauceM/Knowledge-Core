namespace go knowledge

include "common.thrift"

service KnowledgeService {
  common.PingResponse Ping(1: common.PingRequest request)
}
