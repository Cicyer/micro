
生成protobugf
旧版本protoc 目前仅0.0.4
protoc --go_out=plugins=grpc:. *.proto

新版本
protoc --go-grpc_out=. *.proto
protoc --go_out=. *.proto
