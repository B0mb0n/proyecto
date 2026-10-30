package shared

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

// Datos de este servicio, tomados de las variables de entorno del docker-compose.yml.
var (
	consulAddr  = os.Getenv("CONSUL_HTTP_ADDR") // ej. http://consul:8500
	serviceName = os.Getenv("SERVICE_NAME")     // ej. port_scan (el módulo que atiende)
	instanceID  = os.Getenv("INSTANCE_ID")      // ej. port-scan-1 (también es su hostname en Docker)
	servicePort = os.Getenv("SERVICE_PORT")     // ej. 9001
)

// RegisterWithConsul da de alta esta instancia en el registry de Consul, con su
// health check, y la da de baja cuando el contenedor se apaga (docker stop).
func RegisterWithConsul() {
	port, _ := strconv.Atoi(servicePort)

	payload, _ := json.Marshal(map[string]any{
		"ID":      instanceID,
		"Name":    serviceName,
		"Address": instanceID,
		"Port":    port,
		// Consul consulta el health check gRPC estándar (grpc.health.v1) cada 5s.
		// Si falla y sigue fallando 1 minuto, borra la instancia del registry
		// automáticamente (por si el contenedor muere sin avisar).
		"Check": map[string]any{
			"GRPC":                           fmt.Sprintf("%s:%d", instanceID, port),
			"Interval":                       "5s",
			"Timeout":                        "2s",
			"DeregisterCriticalServiceAfter": "1m",
		},
	})

	// Consul puede tardar unos segundos en estar listo: reintenta.
	for i := 0; i < 10; i++ {
		err := consulPut("/v1/agent/service/register", payload)
		if err == nil {
			log.Printf("registrado en Consul: %s (%s)", instanceID, serviceName)
			break
		}
		log.Printf("Consul no disponible todavía: %v", err)
		time.Sleep(2 * time.Second)
	}

	// Al recibir SIGTERM/SIGINT, se da de baja del registry y sale.
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		consulPut("/v1/agent/service/deregister/"+instanceID, nil)
		log.Printf("dado de baja en Consul: %s", instanceID)
		os.Exit(0)
	}()
}

// consulPut hace un PUT a la API de Consul y regresa error si no responde 200.
func consulPut(path string, body []byte) error {
	req, _ := http.NewRequest(http.MethodPut, consulAddr+path, bytes.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Consul respondió %d", resp.StatusCode)
	}
	return nil
}
