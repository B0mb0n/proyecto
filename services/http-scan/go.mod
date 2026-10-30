module httpscan

go 1.22.2

require (
	github.com/lib/pq v1.12.3
	google.golang.org/grpc v1.64.1
	monolito/proto v0.0.0
	monolito/shared v0.0.0
)

replace (
	monolito/proto => ../../proto
	monolito/shared => ../shared
)
