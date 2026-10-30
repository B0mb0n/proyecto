package shared

import (
	"log"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// ServeGRPC levanta el servidor gRPC de un servicio en SERVICE_PORT.
// `register` es donde cada servicio registra su implementación de ScanService.
// Además expone el health check estándar de gRPC (lo usa Consul) y reflection
// (permite probar con grpcurl sin pasarle el .proto).
func ServeGRPC(register func(*grpc.Server)) {
	lis, err := net.Listen("tcp", ":"+servicePort)
	if err != nil {
		log.Fatal(err)
	}

	s := grpc.NewServer()
	register(s)
	healthpb.RegisterHealthServer(s, health.NewServer())
	reflection.Register(s)

	RegisterWithConsul() // alta en Consul (con health check) y baja al apagarse

	log.Printf("%s escuchando gRPC en :%s", instanceID, servicePort)
	log.Fatal(s.Serve(lis))
}
