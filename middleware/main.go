package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	pb "monolito/proto/scanpb"
	"monolito/shared"
)

// Define la estructura exacta del JSON que enviará el frontend cuando el usuario pulse el botón de escanear.

type scanRequest struct {
	Target  string   `json:"target"`
	Modules []string `json:"modules"`
}

var lb = NewLoadBalancer() // Crea la instancia global del balanceador de carga utilizando el algoritmo Round Robin que esta en loadbalancer.

// Conexiones gRPC abiertas, una por instancia (dirección host:port). Se reutilizan entre peticiones.

var conns sync.Map

// forwardToService llama por gRPC (ScanService.Scan) a la instancia elegida y regresa lo que respondió.

func forwardToService(ctx context.Context, addr string, target string) (*pb.ScanResponse, error) {
	c, ok := conns.Load(addr)
	if !ok {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials())) // La conexión real se abre en la primera llamada.
		if err != nil {
			return nil, err
		}
		c, _ = conns.LoadOrStore(addr, conn)
	}

	return pb.NewScanServiceClient(c.(*grpc.ClientConn)).Scan(ctx, &pb.ScanRequest{Target: target})
}

// enableCORS permite que el frontend (en otro contenedor/puerto) pueda llamar a este middleware desde el navegador.

func enableCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "http://localhost:3000") // Autoriza únicamente al origen especificado.
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS") // Especifica que los métodos permitidos son GET, POST y OPTIONS.
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

// Si las reglas de CORS se cumplen, permite que la petición continúe hacia la función principal scanHandler.

		if r.Method == http.MethodOptions {
			return
		}
		next(w, r)
	}
}

func scanHandler(w http.ResponseWriter, r *http.Request) {

	// Valida que la ruta de escaneo únicamente acepte peticiones de tipo POST.

	if r.Method != http.MethodPost {
		http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
		return
	}

	var req scanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "JSON inválido", http.StatusBadRequest)
		return
	}

	// Arreglo donde se irán acumulando las respuestas que devuelvan cada uno de los servicios ejecutados.

	var results []*pb.ScanResponse

	// Si un servicio no contesta en 5s, la llamada gRPC se cancela.

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Recorre uno por uno los módulos que el usuario seleccionó en la interfaz

	for _, module := range req.Modules {

		// 1. Service discovery (consul): ¿qué instancias sanas hay para este módulo?
		instances := healthyInstances(module)

		// 2. Load balancing: ¿a cuál de ellas le toca esta petición?
		instance, ok := lb.Next(module, instances)
		if !ok {
			http.Error(w, "módulo sin instancias disponibles: "+module, http.StatusServiceUnavailable)
			return
		}

		// Indicando a qué módulo, réplica y dirección se le está desviando el trabajo.

		log.Printf("dispatch: %s -> %s (%s)", module, instance.Name, instance.Addr)

		// 3. Reenviar la petición real al servicio elegido (gRPC).
		result, err := forwardToService(ctx, instance.Addr, req.Target)
		if err != nil {
			log.Printf("error gRPC en %s (%s): %v", module, instance.Name, err)
			http.Error(w, "error llamando a "+module, http.StatusBadGateway)
			return
		}
		results = append(results, result)
	}

	w.Header().Set("Content-Type", "application/json")	// Le avisa al navegador que la respuesta que va a recibir está estructurada como un JSON.
	json.NewEncoder(w).Encode(results)			// Convierte todos los resultados acumulados de los escaneos en formato JSON y los envía de regreso al cliente (el frontend).
}

// ---------- Cola asíncrona (Kafka) ----------

// Solo estos módulos tienen cola

var validModules = map[string]bool{"port_scan": true, "http_scan": true, "tls_scan": true}

// Resultados recibidos de scan.results, agrupados por job. Todas las réplicas del middleware
// leen todos los resultados así que el frontend puede consultar cualquiera de ellas.

var (
	mu   sync.Mutex
	jobs = map[string][]*pb.ScanResult{}
)

// createJobHandler encola un ScanJob (protobuf) por módulo en scan.<módulo> y responde de inmediato con el job_id.

func createJobHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
		return
	}

	var req scanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "JSON inválido", http.StatusBadRequest)
		return
	}
	for _, module := range req.Modules {
		if !validModules[module] {
			http.Error(w, "módulo desconocido: "+module, http.StatusBadRequest)
			return
		}
	}

	idBytes := make([]byte, 6)
	rand.Read(idBytes)
	jobID := hex.EncodeToString(idBytes)

	for _, module := range req.Modules {
		msg, _ := proto.Marshal(&pb.ScanJob{JobId: jobID, Target: req.Target})
		if err := shared.Publish("scan."+module, jobID, msg); err != nil {
			log.Printf("error publicando en scan.%s: %v", module, err)
			http.Error(w, "kafka no disponible", http.StatusBadGateway)
			return
		}
		log.Printf("encolado: job %s -> scan.%s", jobID, module)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"job_id": jobID})
}

// jobHandler devuelve los resultados que han llegado hasta ahora para un job.

func jobHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	results := append([]*pb.ScanResult{}, jobs[r.PathValue("id")]...)
	mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func main() {
	// Cada réplica usa su hostname como consumer group para recibir TODOS los resultados.
	hostname, _ := os.Hostname()
	go shared.Consume(shared.ResultsTopic, hostname, false, func(value []byte, _ int, _ int64) {
		res := &pb.ScanResult{}
		if err := proto.Unmarshal(value, res); err != nil {
			return
		}
		mu.Lock()
		jobs[res.GetJobId()] = append(jobs[res.GetJobId()], res)
		mu.Unlock()
	})

	// Multiplexor para gestionar las rutas.
	mux := http.NewServeMux()
	mux.HandleFunc("/scan", enableCORS(scanHandler))      // síncrono: Consul + load balancer + gRPC
	mux.HandleFunc("/jobs", enableCORS(createJobHandler)) // asíncrono: encola en Kafka
	mux.HandleFunc("/jobs/{id}", enableCORS(jobHandler))  // consulta de resultados

	log.Println("middleware escuchando en :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
