package main

import (
	"context"
	"database/sql"
	"log"

	_ "github.com/lib/pq"
	"google.golang.org/grpc"

	pb "monolito/proto/scanpb"
	"monolito/shared"
)

// server implementa el contrato gRPC ScanService (definido en proto/scan.proto).
type server struct {
	pb.UnimplementedScanServiceServer
	repo ResultRepository
}

// Scan es el RPC que el middleware llama para pedir el escaneo.
func (s *server) Scan(ctx context.Context, req *pb.ScanRequest) (*pb.ScanResponse, error) {

	// Escaneo simulado.

	result := ScanResult{
		Module: "port_scan",
		Target: req.GetTarget(),
		Output: "Simulado: 22/tcp OPEN, 80/tcp OPEN, 443/tcp OPEN",
	}

	if err := s.repo.Save(result); err != nil {
		log.Printf("error guardando resultado: %v", err)
	}

	return &pb.ScanResponse{Module: result.Module, Target: result.Target, Output: result.Output}, nil
}

func main() {

	// Conexión a la base de datos compartida.

	dsn := "host=db port=5432 user=postgres password=postgres dbname=monolito sslmode=disable"
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	srv := &server{repo: NewPostgresRepository(db)}

	// Consume la cola scan.port_scan (Kafka) reutilizando el mismo Scan del contrato gRPC.
	shared.StartWorker(func(target string) string {
		resp, _ := srv.Scan(context.Background(), &pb.ScanRequest{Target: target})
		return resp.GetOutput()
	})

	shared.ServeGRPC(func(s *grpc.Server) { pb.RegisterScanServiceServer(s, srv) })
}
