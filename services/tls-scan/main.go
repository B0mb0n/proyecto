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
	db *sql.DB
}

// Scan es el RPC que el middleware llama para pedir el escaneo.
func (s *server) Scan(ctx context.Context, req *pb.ScanRequest) (*pb.ScanResponse, error) {

	// La fábrica construye cada checker por nombre.

	checks := []string{"cert_validity", "protocol_version"}
	output := ""
	for i, kind := range checks {
		checker := NewChecker(kind)
		if i > 0 {
			output += " | "
		}
		output += checker.Run(req.GetTarget())
	}

	result := &pb.ScanResponse{Module: "tls_scan", Target: req.GetTarget(), Output: output}

	_, err := s.db.Exec(
		`INSERT INTO scan_results (module, target, output) VALUES ($1, $2, $3)`,
		result.Module, result.Target, result.Output,
	)
	if err != nil {
		log.Printf("error guardando resultado: %v", err)
	}

	return result, nil
}

func main() {
	dsn := "host=db port=5432 user=postgres password=postgres dbname=monolito sslmode=disable"
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	srv := &server{db: db}

	// Consume la cola scan.tls_scan (Kafka) reutilizando el mismo Scan del contrato gRPC.
	shared.StartWorker(func(target string) string {
		resp, _ := srv.Scan(context.Background(), &pb.ScanRequest{Target: target})
		return resp.GetOutput()
	})

	shared.ServeGRPC(func(s *grpc.Server) { pb.RegisterScanServiceServer(s, srv) })
}
