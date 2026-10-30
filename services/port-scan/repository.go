package main

import "database/sql"

// ScanResult es el resultado que produce este servicio.

type ScanResult struct {
	Module string `json:"module"`
	Target string `json:"target"`
	Output string `json:"output"`
}

// ResultRepository es el CONTRATO (interfaz) para guardar resultados.
// Ningún otro código del servicio sabe si detrás hay Postgres, un archivo, o memoria
// Solo conoce estos 2 métodos. Esto es el corazón del Repository Pattern: separar "qué se guarda" de "cómo y dónde se guarda".

type ResultRepository interface {
	Save(result ScanResult) error
}

// PostgresRepository es la implementación concreta que sí sabe hablar con Postgres.
type PostgresRepository struct {
	DB *sql.DB
}

func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{DB: db}
}

func (r *PostgresRepository) Save(result ScanResult) error {
	query := `INSERT INTO scan_results (module, target, output) VALUES ($1, $2, $3)`
	_, err := r.DB.Exec(query, result.Module, result.Target, result.Output)
	return err
}
