package main

import "sync"

// LoadBalancer reparte las peticiones entre varias instancias del  mismo módulo usando Round Robin
// La primera petición va a la instancia 0, la segunda a la 1, la tercerra otra vez a la 0, etc.

type LoadBalancer struct {
	mu      sync.Mutex     		// Candado de exclusión mutua. Este candado evita que dos hilos modifiquen el contador al mismo tiempo y corrompan los datos.
	counter map[string]int 		// Mapa que lleva la cuenta de cuántas peticiones se han enviado a cada módulo
}

// Función constructora que inicializa el balanceador de carga creando un mapa de contadores vacío listo para usarse.

func NewLoadBalancer() *LoadBalancer {
	return &LoadBalancer{counter: map[string]int{}}
}

// Next elige la siguiente instancia sana para un módulo. Si no hay ninguna instancia sana, regresa false.

func (lb *LoadBalancer) Next(module string, instances []Instance) (Instance, bool) {

	// Si la lista de instancias sanas está vacía, la función no puede rutear nada

	if len(instances) == 0 {
		return Instance{}, false
	}

	// Bloquea el acceso al contador para que sea una operación segura para hilos concurrentes, asegurando que se libere al terminar la función.

	lb.mu.Lock()
	defer lb.mu.Unlock()

	i := lb.counter[module] % len(instances)
	lb.counter[module]++

	return instances[i], true
}
