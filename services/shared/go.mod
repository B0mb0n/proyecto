module monolito/shared

go 1.22.2

require (
	github.com/segmentio/kafka-go v0.4.47
	google.golang.org/grpc v1.64.1
	google.golang.org/protobuf v1.34.2
	monolito/proto v0.0.0
)

replace monolito/proto => ../../proto
