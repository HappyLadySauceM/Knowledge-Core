module github.com/HappyLadySauce/Knowledge-Core

go 1.26.1

// Hertz's Thrift model generator and registry-etcd v0.3.0 require the
// pre-context Apache Thrift API.
replace github.com/apache/thrift => github.com/apache/thrift v0.13.0
