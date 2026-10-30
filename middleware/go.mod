module gateway

go 1.22.2

require (
	google.golang.org/grpc v1.64.1
	google.golang.org/protobuf v1.34.2
	monolito/proto v0.0.0
	monolito/shared v0.0.0
)

replace (
	monolito/proto => ../proto
	monolito/shared => ../services/shared
)
