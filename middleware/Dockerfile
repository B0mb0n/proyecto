FROM golang:1.22-alpine AS builder
# protoc + plugins: el código gRPC se genera aquí desde proto/scan.proto
RUN apk add --no-cache protobuf \
 && go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2 \
 && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
WORKDIR /app
COPY proto ./proto
RUN protoc -I proto --go_out=proto --go_opt=module=monolito/proto \
    --go-grpc_out=proto --go-grpc_opt=module=monolito/proto proto/scan.proto
COPY services/shared ./services/shared
COPY middleware ./middleware
WORKDIR /app/middleware
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -o gateway .

FROM alpine:3.20
WORKDIR /app
COPY --from=builder /app/middleware/gateway .
EXPOSE 8080
CMD ["./gateway"]
