#!/bin/bash
# setup.sh — instala lo necesario y levanta el proyecto.
# Probado en Debian/Ubuntu/Kali. Requiere sudo.

set -e  # si un comando falla, el script se detiene (no sigue a ciegas)

echo "== 1. Verificando Docker =="
if ! command -v docker &> /dev/null; then
    echo "Docker no encontrado. Instalando..."
    sudo apt update
    sudo apt install -y docker.io
    sudo systemctl enable docker
else
    echo "Docker ya está instalado: $(docker --version)"
fi

echo "== 2. Asegurando que el daemon esté corriendo =="
sudo systemctl start docker

echo "== 3. Verificando Docker Compose =="
if docker compose version &> /dev/null; then
    COMPOSE_CMD="docker compose"
    echo "Usando plugin docker compose (V2)."
elif command -v docker-compose &> /dev/null; then
    COMPOSE_CMD="docker-compose"
    echo "Usando binario docker-compose ya instalado."
else
    echo "Docker Compose no encontrado. Instalando binario standalone..."
    sudo curl -SL "https://github.com/docker/compose/releases/latest/download/docker-compose-linux-x86_64" \
        -o /usr/local/bin/docker-compose
    sudo chmod +x /usr/local/bin/docker-compose
    COMPOSE_CMD="docker-compose"
fi

echo "== 4. Permitiendo usar Docker sin sudo (requiere reiniciar sesión) =="
sudo usermod -aG docker "$USER" || true

echo "== 5. Levantando el proyecto =="
$COMPOSE_CMD up --build -d

echo
echo "Listo. Servicios disponibles:"
echo "  Frontend:   http://localhost:3000"
echo "  Middleware: http://localhost:8080/scan"
echo
echo "Prueba rápida:"
echo '  curl -X POST http://localhost:8080/scan -d '"'"'{"target":"192.168.1.20","modules":["port_scan","http_scan","tls_scan"]}'"'"''
