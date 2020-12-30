
生成protobugf
<br>
旧版本protoc 目前仅支持到0.0.5
<br>
protoc --go_out=plugins=grpc:. *.proto
<br>
新版本
<br>
protoc --go-grpc_out=. *.proto
<br>
protoc --go_out=. *.proto
