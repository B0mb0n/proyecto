package shared

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/proto"

	pb "monolito/proto/scanpb"
)

// Tópicos de Kafka: uno por servicio (cola de trabajos) + uno común para resultados.
// El nombre del tópico de un servicio es "scan." + SERVICE_NAME (ej. scan.port_scan).
const ResultsTopic = "scan.results"

var (
	brokers = []string{os.Getenv("KAFKA_BROKERS")} // ej. kafka:9092

	// Un solo productor para todo el proceso; el tópico se indica en cada mensaje.
	// Balancer Hash: la misma key (job_id) siempre cae en la misma partición.
	writer = &kafka.Writer{Addr: kafka.TCP(brokers...), Balancer: &kafka.Hash{}, BatchTimeout: 10 * time.Millisecond}
)

// Publish envía un mensaje a un tópico.
func Publish(topic, key string, value []byte) error {
	return writer.WriteMessages(context.Background(), kafka.Message{Topic: topic, Key: []byte(key), Value: value})
}

// Consume se une a un consumer group y llama a handle por cada mensaje (bloquea para siempre).
// Las réplicas con el mismo group se reparten las particiones; grupos distintos reciben todo.
// El offset se confirma DESPUÉS de procesar el mensaje (entrega "al menos una vez").
func Consume(topic, group string, fromStart bool, handle func(value []byte, partition int, offset int64)) {
	start := kafka.LastOffset
	if fromStart {
		start = kafka.FirstOffset // un grupo nuevo lee lo que ya estaba encolado
	}
	r := kafka.NewReader(kafka.ReaderConfig{Brokers: brokers, Topic: topic, GroupID: group, StartOffset: start})
	defer r.Close()

	for {
		msg, err := r.FetchMessage(context.Background())
		if err != nil {
			log.Printf("kafka %s: %v", topic, err)
			time.Sleep(2 * time.Second)
			continue
		}
		handle(msg.Value, msg.Partition, msg.Offset)
		if err := r.CommitMessages(context.Background(), msg); err != nil {
			log.Printf("kafka %s: error confirmando offset: %v", topic, err)
		}
	}
}

// StartWorker convierte a este servicio en consumidor de su cola: por cada ScanJob
// ejecuta scan(target) (que escanea y guarda en la BD) y publica el ScanResult.
// Los mensajes van serializados con protobuf (mismo contrato que usa gRPC).
func StartWorker(scan func(target string) string) {
	go Consume("scan."+serviceName, serviceName, true, func(value []byte, partition int, offset int64) {
		job := &pb.ScanJob{}
		if err := proto.Unmarshal(value, job); err != nil {
			log.Printf("job inválido: %v", err)
			return
		}
		log.Printf("job %s: %s (partición %d, offset %d)", job.GetJobId(), job.GetTarget(), partition, offset)

		out, _ := proto.Marshal(&pb.ScanResult{
			JobId: job.GetJobId(), Module: serviceName, Target: job.GetTarget(), Output: scan(job.GetTarget()),
			Worker: instanceID, Partition: int32(partition), Offset: offset,
		})
		if err := Publish(ResultsTopic, job.GetJobId(), out); err != nil {
			log.Printf("error publicando resultado: %v", err)
		}
	})
}
