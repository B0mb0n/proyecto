package main

type CheckStrategy interface {
	Check(target string) string
}

// HeaderCheckStrategy simula revisar headers de seguridad.

type HeaderCheckStrategy struct{}

func (HeaderCheckStrategy) Check(target string) string {
	return "Falta header X-Content-Type-Options"
}

// runStrategies ejecuta una lista de estrategias y junta sus salidas.
// Agregar una verificación nueva = agregar una struct nueva a esta lista, sin tocar el resto del servicio.

func runStrategies(target string, strategies []CheckStrategy) string {
	output := ""
	for i, s := range strategies {
		if i > 0 {
			output += " | "
		}
		output += s.Check(target)
	}
	return output
}
