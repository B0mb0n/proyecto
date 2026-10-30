-- Se guarda una fila por cada escaneo que se ejecuta, en una sola tabla compartida por los 3 servicios: scan_results

CREATE TABLE IF NOT EXISTS scan_results (
    id         SERIAL PRIMARY KEY,
    module     VARCHAR(50) NOT NULL, 	-- Qué tipo de escaneo lo generó
    target     VARCHAR(255) NOT NULL, 	-- La IP/host que se escaneó
    output     TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);
