package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
)

// Instance es una réplica concreta de un servicio.
type Instance struct {
	Name string
	Addr string // host:port al que se conecta el cliente gRPC
}

var consulAddr = os.Getenv("CONSUL_HTTP_ADDR") // ej. http://consul:8500

// healthyInstances le pregunta a Consul qué instancias del servicio `module`
// están pasando su health check. "?passing=true" excluye las que están caídas,
// así que ya no hace falta un archivo estático ni health checks propios.
func healthyInstances(module string) []Instance {
	// PathEscape evita que un "module" malicioso (ej. "../agent") cambie la ruta consultada.
	resp, err := http.Get(consulAddr + "/v1/health/service/" + url.PathEscape(module) + "?passing=true")
	if err != nil {
		log.Printf("no se pudo consultar a Consul: %v", err)
		return nil
	}
	defer resp.Body.Close()

	// Solo se decodifican los campos que se usan de la respuesta de Consul.
	var entries []struct {
		Service struct {
			ID      string
			Address string
			Port    int
		}
	}
	json.NewDecoder(resp.Body).Decode(&entries)

	var instances []Instance
	for _, e := range entries {
		instances = append(instances, Instance{
			Name: e.Service.ID,
			Addr: fmt.Sprintf("%s:%d", e.Service.Address, e.Service.Port),
		})
	}

	// Consul no garantiza el orden de la lista; se ordena para que el round robin reparta parejo.
	sort.Slice(instances, func(i, j int) bool { return instances[i].Name < instances[j].Name })
	return instances
}
