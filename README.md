# monolitoScanner — Vulnerability Scanner distribuido

Escáner de vulnerabilidades con arquitectura distribuida: un frontend, un
reverse proxy / load balancer (nginx), dos réplicas de un middleware que
descubren los servicios en Consul (service discovery dinámico), una cola
de mensajería asíncrona con Apache Kafka y 3
microservicios de backend, cada uno con su propio patrón de diseño y su
propia conexión a base de datos. Orquestado completamente con Docker Compose.

La comunicación **interna** entre el middleware (load balancer) y los servicios
de backend usa **gRPC con Protocol Buffers** (contrato en `proto/scan.proto`).
Solo el tramo navegador → nginx → middleware sigue siendo HTTP/JSON, porque el
navegador no habla gRPC.

> **Nota:** los 3 módulos de escaneo (`port_scan`, `http_scan`, `tls_scan`)
> devuelven resultados **simulados** por ahora — el propósito actual del
> proyecto es la arquitectura distribuida (discovery, balanceo, patrones
> de diseño), no el escaneo real todavía.

## Arquitectura

```
┌────────────────────────────────────────────────────────────┐
│  FRONTEND (nginx:alpine, puerto host 3000)                  │
│  index.html + style.css + script.js                         │
│  fetch() → POST /jobs, GET /jobs/{id} (cola Kafka)          │
└──────────────────────────┬─────────────────────────────────┘
                           ▼
┌────────────────────────────────────────────────────────────┐
│  NGINX GATEWAY (reverse proxy + load balancer, host 8080)   │
│  upstream middleware_pool → round robin, max_fails=3        │
│  Único punto de entrada: los middlewares ya no son públicos │
└───────────────┬─────────────────────────┬──────────────────┘
                ▼                         ▼
        ┌───────────────┐         ┌───────────────┐
        │ middleware-1  │         │ middleware-2  │
        │ consulta a    │         │ consulta a    │
        │ Consul +      │         │ Consul +      │
        │ round robin   │         │ round robin   │
        └───────┬───────┘         └───────┬───────┘
                └────────────┬────────────┘
        ┌────────────────────┼────────────────────┐
        ▼                    ▼                    ▼
 ┌─────────────┐      ┌─────────────┐      ┌─────────────┐
 │ port-scan-1 │      │ http-scan-1 │      │ tls-scan-1  │
 │ port-scan-2 │      │ (Strategy)  │      │ (Factory)   │
 │ (Repository)│      └──────┬──────┘      └──────┬──────┘
 └──────┬──────┘             │                    │
        └────────────────────┴────────────────────┘
                             │ INSERT INTO scan_results
                             ▼
                  ┌─────────────────────┐
                  │  Postgres (db)       │
                  └─────────────────────┘
```

10 contenedores: `frontend`, `nginx-gateway`, `middleware-1`, `middleware-2`,
`consul`, `port-scan-1`, `port-scan-2`, `http-scan-1`, `tls-scan-1`, `db`.

`consul` no está en el camino de las peticiones de escaneo: es el
directorio al que los backends se registran y que los middlewares consultan
(ver la sección "Service discovery con Consul").

Hay **dos niveles de balanceo**:

| Nivel | Quién | Reparte entre | Cómo |
|---|---|---|---|
| 1 | `nginx-gateway` | `middleware-1` y `middleware-2` | `upstream` con round robin |
| 2 | cada `middleware` | las réplicas sanas de cada servicio (ej. `port-scan-1/2`), según Consul | round robin en Go, llamada **gRPC** |

| Tramo | Protocolo |
|---|---|
| frontend → nginx-gateway → middleware | HTTP/JSON |
| middleware (load balancer) → servicios de backend | **gRPC + protobuf** |
| Consul → servicios de backend (health check) | **gRPC** (`grpc.health.v1`) |

## Reverse proxy / Load balancer (nginx)

Corre en su propio contenedor (`nginx-gateway/`), entre el frontend y los
middlewares. El frontend ya no llama al middleware directamente: llama a
nginx, y nginx reenvía.

`nginx-gateway/nginx.conf`:

```nginx
upstream middleware_pool {
    server middleware-1:8080 max_fails=3 fail_timeout=30s;
    server middleware-2:8080 max_fails=3 fail_timeout=30s;
}

server {
    listen 80;
    location /scan {
        proxy_pass http://middleware_pool;
        ...
    }
    location /health { return 200 "nginx gateway alive"; }
}
```

- `upstream middleware_pool`: el grupo de servidores detrás de un solo
  nombre. Sin otra directiva, nginx usa **round robin**.
- `max_fails=3 fail_timeout=30s`: health check **pasivo**. Si una réplica
  falla 3 veces, nginx no le manda tráfico durante 30s. (El health check
  activo, como el que hace Consul con los backends, no existe en nginx
  open source.)
- `proxy_pass`: reenvía la petición al pool.
- `proxy_set_header ...`: conserva la IP original del cliente
  (`X-Real-IP`, `X-Forwarded-For`).
- `add_header X-Upstream $upstream_addr`: devuelve en la respuesta qué
  middleware atendió la petición (útil para demostrar el balanceo).

El puerto del host no cambió (`8080`), por eso `script.js` no se tocó.

## Service discovery con Consul

Reemplaza al `services.json` estático y a los health checks que hacía el
middleware. Corre en su propio contenedor (`consul`, imagen oficial
`hashicorp/consul`, modo `-dev`: un solo nodo, en memoria). Su interfaz web
está en **http://localhost:8500/ui**.

```
 ① alta al arrancar        ┌────────────┐   ③ GET /v1/health/service/<módulo>?passing=true
 backends ───────────────▶ │            │ ◀───────────────────────────────── middlewares
 ② health check c/5s  ◀─── │   CONSUL   │ ④ responde solo las instancias sanas
 ⑤ baja al apagarse ─────▶ │            │
                           └────────────┘
```

**1. Registro (alta).** Al arrancar, cada backend se da de alta llamando a
`shared.RegisterWithConsul()`, con una petición
`PUT /v1/agent/service/register`. Ese código vive **una sola vez** en
`services/shared/consul.go`, un módulo Go compartido que importan los 3
servicios (cada uno lo configura con sus variables de entorno):

```go
"ID":      instanceID,   // port-scan-1
"Name":    serviceName,  // port_scan  (el módulo; es la clave de búsqueda)
"Address": instanceID,   // Docker resuelve el nombre del contenedor
"Port":    port,
```

**2. Health check.** En ese mismo registro se declara el chequeo. Consul
(no el middleware) consulta el health check gRPC estándar (`grpc.health.v1`)
de cada instancia cada 5s:

```go
"Check": map[string]any{
    "GRPC":                           "port-scan-1:9001",
    "Interval":                       "5s",
    "Timeout":                        "2s",
    "DeregisterCriticalServiceAfter": "1m",
}
```

Si una instancia deja de responder, Consul la marca como *critical* y deja
de reportarla como sana. Si sigue así 1 minuto, **la borra del registry sola**
(`DeregisterCriticalServiceAfter`), útil cuando un contenedor muere sin avisar.

**3. Baja (borrado del registry).** Cuando el contenedor se apaga
(`docker stop` envía `SIGTERM`), el backend captura la señal, llama a
`PUT /v1/agent/service/deregister/<id>` y recién entonces termina. Así Consul
deja de ofrecerlo de inmediato, sin esperar al health check.

**4. Descubrimiento.** El middleware (`middleware/consul.go`) le pregunta a
Consul por las instancias de un módulo en cada petición. `?passing=true`
excluye las que están caídas:

```go
http.Get(consulAddr + "/v1/health/service/" + url.PathEscape(module) + "?passing=true")
```

Con esa lista, el `LoadBalancer` (`loadbalancer.go`) hace el round robin
igual que antes. `url.PathEscape` evita que un nombre de módulo malicioso
(ej. `../agent/self`) cambie la ruta que se consulta en Consul, y la lista se
ordena porque Consul no garantiza el orden entre consultas.

**Variables de entorno** (definidas en `docker-compose.yml`):

| Variable | Dónde | Ejemplo |
|---|---|---|
| `CONSUL_HTTP_ADDR` | backends y middlewares | `http://consul:8500` |
| `SERVICE_NAME` | backends | `port_scan` |
| `INSTANCE_ID` | backends | `port-scan-1` |
| `SERVICE_PORT` | backends | `9001` |

Cada `go.mod` de servicio apunta al módulo compartido con
`replace monolito/shared => ../shared`. Por eso el `build` de esos
contenedores en `docker-compose.yml` usa `context: ./services` (para que el
Dockerfile pueda copiar `shared/` junto con el servicio).

Agregar una réplica nueva solo requiere declararla en `docker-compose.yml`
con su `INSTANCE_ID`: se registra sola y el middleware la descubre sin
tocar ningún archivo de configuración.

## Comunicación gRPC (protobuf)

El contrato vive en `proto/scan.proto` y lo implementan los 3 servicios:

```proto
service ScanService {
  rpc Scan(ScanRequest) returns (ScanResponse);
}
message ScanRequest  { string target = 1; }
message ScanResponse { string module = 1; string target = 2; string output = 3; }
```

- **Servicios** (`port-scan`, `http-scan`, `tls-scan`): cada uno implementa
  `ScanService.Scan` en su `main.go`, conservando su patrón de diseño. El
  servidor gRPC (puerto, health check y reflection) se levanta con
  `shared.ServeGRPC` (`services/shared/grpc.go`).
- **Middleware / load balancer**: tras elegir la instancia (Consul + round
  robin), `forwardToService` (`middleware/main.go`) le llama `Scan` por gRPC.
  Mantiene una conexión por instancia y cada llamada tiene timeout de 5s.
- **Código generado**: no se guarda en el repo. Cada `Dockerfile` ejecuta
  `protoc` sobre `proto/scan.proto` durante el build (genera `proto/scanpb/`),
  así que solo necesitas Docker. Si quieres el código generado en local
  (editor/IDE), instala `protoc`, `protoc-gen-go` y `protoc-gen-go-grpc`, y:
  ```bash
  protoc -I proto --go_out=proto --go_opt=module=monolito/proto \
    --go-grpc_out=proto --go-grpc_opt=module=monolito/proto proto/scan.proto
  ```
  y luego `go mod tidy` dentro de cada módulo.
- Los servicios publican su puerto gRPC al host solo para poder probar con
  grpcurl: `port-scan-1` → `9001`, `http-scan-1` → `9002`, `tls-scan-1` →
  `9003`, `port-scan-2` → `9004`.

## Probar con grpcurl

Instalar [grpcurl](https://github.com/fullstorydev/grpcurl) (`brew install grpcurl`,
o `go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest`, o el binario
de sus releases). Los servidores tienen *reflection*, así que no hace falta
pasar el `.proto`. No usan TLS, por eso `-plaintext`.

> **Estado de estas pruebas:** las salidas de abajo son las **esperadas según
> el código**; el entorno donde se escribió este cambio no tenía acceso a
> Docker ni al proxy de módulos de Go, así que no se pudieron ejecutar.
> Al correrlas, confirma (o pega) la salida real en cada bloque.

Primero levanta todo y espera ~10s a que los servicios se registren:
```bash
docker-compose up --build -d
```

**1. Listar los servicios gRPC expuestos** (reflection):
```bash
grpcurl -plaintext localhost:9001 list
```
Esperado (entre otros): `grpc.health.v1.Health` y `scan.ScanService`.

**2. Describir el contrato:**
```bash
grpcurl -plaintext localhost:9001 describe scan.ScanService
```
Esperado:
```
scan.ScanService is a service:
service ScanService {
  rpc Scan ( .scan.ScanRequest ) returns ( .scan.ScanResponse );
}
```

**3. Health check gRPC** (el mismo que usa Consul):
```bash
grpcurl -plaintext localhost:9001 grpc.health.v1.Health/Check
```
Esperado:
```json
{
  "status": "SERVING"
}
```

**4. Llamar `Scan` directo a cada servicio:**
```bash
grpcurl -plaintext -d '{"target":"192.168.1.20"}' localhost:9001 scan.ScanService/Scan   # port-scan-1
grpcurl -plaintext -d '{"target":"192.168.1.20"}' localhost:9004 scan.ScanService/Scan   # port-scan-2
grpcurl -plaintext -d '{"target":"192.168.1.20"}' localhost:9002 scan.ScanService/Scan   # http-scan-1
grpcurl -plaintext -d '{"target":"192.168.1.20"}' localhost:9003 scan.ScanService/Scan   # tls-scan-1
```
Esperado (`port-scan-1` y `port-scan-2` responden igual):
```json
{ "module": "port_scan", "target": "192.168.1.20", "output": "Simulado: 22/tcp OPEN, 80/tcp OPEN, 443/tcp OPEN" }
{ "module": "http_scan", "target": "192.168.1.20", "output": "Falta header X-Content-Type-Options" }
{ "module": "tls_scan",  "target": "192.168.1.20", "output": "Certificado válido | TLS 1.2 soportado, TLS 1.3 soportado" }
```
(grpcurl los imprime con formato multilínea; aquí se compactaron.)

**5. Comunicación load balancer → servicios** (frontend/curl → nginx →
middleware → gRPC). Cada escaneo es un RPC `Scan` del middleware al servicio
elegido; el log `dispatch` muestra la réplica a la que le tocó:
```bash
for i in 1 2 3 4; do
  curl -s -X POST http://localhost:8080/scan \
    -d '{"target":"192.168.1.20","modules":["port_scan"]}'; echo
done
docker-compose logs middleware-1 middleware-2 | grep "dispatch: port_scan"
```
Esperado: 4 respuestas JSON con `"module":"port_scan"` y, en los logs, el
destino alternando entre `port-scan-1 (port-scan-1:9001)` y
`port-scan-2 (port-scan-2:9001)` (la dirección ya es `host:puerto`, sin
`http://`).

**6. Tolerancia a fallos** — con `port-scan-2` detenido, el LB sigue
respondiendo con `port-scan-1` y el puerto gRPC de `port-scan-2` deja de
contestar:
```bash
docker-compose stop port-scan-2
grpcurl -plaintext -d '{"target":"192.168.1.20"}' localhost:9004 scan.ScanService/Scan   # falla: connection refused
curl -s -o /dev/null -w "%{http_code}\n" -X POST http://localhost:8080/scan \
  -d '{"target":"192.168.1.20","modules":["port_scan"]}'                                 # 200
docker-compose start port-scan-2
```

## Cola de mensajería asíncrona (Kafka)

Además del camino síncrono (`POST /scan`), el backend tiene una **cola de
mensajería asíncrona con Apache Kafka**. El middleware ya no espera a que el
escaneo termine: publica un trabajo en la cola del servicio y responde de
inmediato con un `job_id`; el servicio lo toma cuando puede, escanea, guarda en
Postgres y publica el resultado.

```
 Frontend ──POST /jobs──▶ nginx ──▶ middleware ──publish──▶ [ scan.port_scan ] ──▶ port-scan-1 / port-scan-2  (grupo port_scan)
    ▲        (202 + job_id)                      │           [ scan.http_scan ] ──▶ http-scan-1              (grupo http_scan)
    │                                            │           [ scan.tls_scan  ] ──▶ tls-scan-1               (grupo tls_scan)
    │                                            │                                       │ escanea + INSERT en Postgres
    └──GET /jobs/{id} (polling)── middleware ◀──consume── [ scan.results ] ◀──publish────┘
```

### Tópicos

| Tópico | Productor | Consumidor (consumer group) | Contenido |
|---|---|---|---|
| `scan.port_scan` | middleware | `port_scan` (port-scan-1 y port-scan-2) | trabajo de escaneo de puertos |
| `scan.http_scan` | middleware | `http_scan` (http-scan-1) | trabajo de escaneo HTTP |
| `scan.tls_scan`  | middleware | `tls_scan` (tls-scan-1) | trabajo de escaneo TLS |
| `scan.results`   | los 3 servicios | cada middleware (un group por réplica) | resultado + qué réplica, partición y offset lo procesó |

- **Un tópico por servicio**: cada servicio solo lee su cola; si uno se cae, los
  trabajos esperan en Kafka y se procesan al volver (se prueba abajo).
- **3 particiones por tópico**: las dos réplicas de `port-scan` están en el mismo
  consumer group, así que Kafka se reparte las particiones entre ellas
  (balanceo de carga hecho por Kafka, no por el middleware).
- La **key** del mensaje es el `job_id`, y el offset se confirma *después* de
  procesar (entrega "al menos una vez").
- `scan.results` lo lee **cada réplica del middleware con su propio group**
  (el hostname del contenedor), así que ambas conocen todos los resultados y el
  frontend puede consultar a cualquiera aunque nginx reparta las peticiones.
- Los tópicos los crea el contenedor de un solo uso `kafka-init` al levantar el
  compose. Kafka corre en modo KRaft (un nodo, sin ZooKeeper) y solo es
  accesible dentro de la red de Docker.

### Nuevos endpoints del middleware

| Endpoint | Respuesta |
|---|---|
| `POST /jobs` con `{"target": "...", "modules": ["port_scan", ...]}` | `202 {"job_id": "a1b2c3d4e5f6"}` — solo encola |
| `GET /jobs/{job_id}` | lista de resultados que han llegado hasta ahora |

`POST /scan` (síncrono, con Consul + load balancer) sigue funcionando igual,
así que las pruebas anteriores siguen siendo válidas. Solo se aceptan los
módulos `port_scan`, `http_scan` y `tls_scan`: así el cliente no puede publicar
en otros tópicos (por ejemplo, falsificando mensajes en `scan.results`).

### Cómo se serializan los mensajes (este repo: gRPC + protobuf)

Los mensajes de Kafka son **protobuf**, definidos en el mismo `proto/scan.proto`
que el contrato gRPC (`ScanJob` y `ScanResult`). Todo el código de Kafka está en
`services/shared/kafka.go` (`Publish`, `Consume`, `StartWorker`). El consumidor
de cada servicio llama al **mismo método `Scan` del contrato gRPC**, así que el
trabajo llega por dos vías (RPC síncrono o cola) y se ejecuta con la misma lógica.

## Probar Kafka

Con todo arriba (`docker-compose up --build`):

**1. Tópicos creados**

```bash
docker exec kafka /opt/kafka/bin/kafka-topics.sh --bootstrap-server kafka:9092 --list
```
Salida esperada: `scan.http_scan`, `scan.port_scan`, `scan.results`, `scan.tls_scan`.

**2. Encolar un trabajo y ver que responde de inmediato**

```bash
curl -s -X POST http://localhost:8080/jobs -H "Content-Type: application/json" \
  -d '{"target":"192.168.1.20","modules":["port_scan","http_scan","tls_scan"]}'
```
Salida esperada: `{"job_id":"..."}` (HTTP 202).

**3. Consultar los resultados** (cambia `JOB_ID`; el primer trabajo tras levantar puede tardar unos segundos mientras los consumidores se unen a su grupo)

```bash
curl -s http://localhost:8080/jobs/JOB_ID
```
Salida esperada: 3 resultados, cada uno con `worker`, `partition` y `offset`.

**4. Ver los mensajes que pasaron por una cola**

```bash
docker exec kafka /opt/kafka/bin/kafka-console-consumer.sh --bootstrap-server kafka:9092 \
  --topic scan.results --from-beginning --timeout-ms 5000
```

**5. Ver cómo se reparten las particiones entre las réplicas de port-scan**

```bash
docker exec kafka /opt/kafka/bin/kafka-consumer-groups.sh --bootstrap-server kafka:9092 \
  --describe --group port_scan
```
Salida esperada: 3 particiones asignadas a consumidores distintos (dos réplicas).

**6. La cola absorbe la caída de un servicio**

```bash
docker stop tls-scan-1
curl -s -X POST http://localhost:8080/jobs -H "Content-Type: application/json" \
  -d '{"target":"10.0.0.5","modules":["tls_scan"]}'     # responde 202 aunque el servicio está caído
docker start tls-scan-1                                   # al volver, consume el trabajo pendiente
curl -s http://localhost:8080/jobs/JOB_ID                 # ya aparece el resultado
```

**7. Quedó guardado en Postgres**

```bash
docker exec monolito-db psql -U postgres -d monolito -c "SELECT module, target FROM scan_results ORDER BY id DESC LIMIT 5;"
```

> Registra aquí tus salidas reales de las pruebas 1–7 antes de entregar.

## Diferencias entre este repo y su par

Este proyecto existe en dos repos con **el mismo diseño** (mismos tópicos,
mismos endpoints, mismo frontend, mismo compose de Kafka) y backends distintos:

| | `monolitoGo` (HTTP) | `monolitoGoProto` (gRPC) |
|---|---|---|
| Llamada síncrona middleware → servicio | `POST /scan` con JSON | RPC `ScanService.Scan` (HTTP/2 + protobuf) |
| Mensajes en Kafka | JSON (`encoding/json`) | protobuf (`ScanJob`, `ScanResult` en `scan.proto`) |
| Contrato | implícito (estructuras Go repetidas en cada servicio) | explícito y único: `proto/scan.proto` |
| Health check en Consul | HTTP `GET /health` | protocolo estándar `grpc.health.v1` |
| Probar un servicio directo | `curl` | `grpcurl` (con reflection) |
| Build de imágenes | contexto `./services` (servicios) y `.` (middleware) | contexto `.`; el Dockerfile genera el código con `protoc` |
| Cómo el servicio atiende la cola | `StartWorker(run)`, `run` también lo usa `POST /scan` | `StartWorker(...)` llama al mismo `Scan` del contrato gRPC |

Lo que **no** cambia: Kafka sustituye la comunicación directa solo en el camino
asíncrono; Consul, nginx y el balanceo del camino síncrono siguen como antes.

## Estructura del proyecto

```
.
├── docker-compose.yml        (incluye kafka y kafka-init)
├── frontend
│   ├── Dockerfile
│   ├── index.html
│   ├── script.js
│   └── style.css
├── middleware
│   ├── consul.go             (descubre instancias sanas en Consul)
│   ├── Dockerfile
│   ├── go.mod
│   ├── loadbalancer.go       (round robin)
│   └── main.go
├── nginx-gateway
│   ├── Dockerfile
│   └── nginx.conf            (reverse proxy + load balancer)
├── proto
│   ├── go.mod
│   └── scan.proto            (contrato gRPC/protobuf; el código se genera en el build)
├── README.md
├── schema.sql
├── services
│   ├── http-scan
│   │   ├── Dockerfile
│   │   ├── go.mod
│   │   ├── go.sum
│   │   ├── main.go
│   │   └── strategy.go
│   ├── port-scan
│   │   ├── Dockerfile
│   │   ├── go.mod
│   │   ├── go.sum
│   │   ├── main.go
│   │   └── repository.go
│   ├── shared
│   │   ├── consul.go           (alta, health check y baja en Consul; lo usan los 3 servicios)
│   │   ├── kafka.go            (Publish, Consume y StartWorker con protobuf; middleware y servicios)
│   │   ├── go.mod
│   │   └── grpc.go             (servidor gRPC + health + reflection; lo usan los 3 servicios)
│   └── tls-scan
│       ├── Dockerfile
│       ├── factory.go
│       ├── go.mod
│       ├── go.sum
│       └── main.go
└── setup.sh

10 directories, 35 files
```

## Requisitos

**El proyecto en sí NO requiere Linux** — corre en cualquier sistema
operativo compatible con Docker (Windows, macOS o Linux), porque todo
corre dentro de contenedores: no necesitas instalar Go, Postgres, ni
nada más en tu máquina, sin importar el sistema operativo.

Lo que **sí es específico de Linux** es el script `setup.sh` incluido
aquí, porque usa `apt` (gestor de paquetes de Debian/Ubuntu/Kali) para
instalar Docker automáticamente si falta. En Windows o macOS, instala
Docker manualmente (ver abajo) y luego levanta el proyecto con
`docker-compose up --build` directamente, sin usar `setup.sh`.

| Sistema operativo | Cómo obtener Docker + Compose |
|---|---|
| **Linux** (Debian/Ubuntu/Kali) | Ejecuta `./setup.sh` — lo instala todo automáticamente |
| **Windows** | Instala [Docker Desktop](https://www.docker.com/products/docker-desktop/) (incluye Compose) |
| **macOS** | Instala [Docker Desktop](https://www.docker.com/products/docker-desktop/) (incluye Compose) |

Además necesitas los puertos **3000**, **8080**, **5432** y **9001–9004**
(gRPC de los servicios, para grpcurl) libres en tu máquina.

## Cómo levantarlo

### En Linux (Debian/Ubuntu/Kali)

```bash
chmod +x setup.sh
./setup.sh
```

El script:
1. Verifica si Docker está instalado; si no, lo instala (`apt install docker.io`).
2. Se asegura de que el servicio de Docker esté corriendo.
3. Verifica si tienes Docker Compose (plugin `docker compose` o binario
   standalone `docker-compose`); si no tienes ninguno, instala el binario.
4. Te agrega al grupo `docker` (para no necesitar `sudo` en el futuro —
   requiere cerrar sesión y volver a entrar para tomar efecto).
5. Levanta todo el proyecto con `--build -d` (en segundo plano).

### En Windows o macOS (con Docker Desktop ya instalado y corriendo)

```bash
docker-compose up --build
```
o, si tu instalación usa el binario standalone con guion:
```bash
docker-compose up --build
```

## Verificar que todo quedó arriba

```bash
docker-compose ps
```
Los 10 servicios deben mostrar `Up` (o `running`). Los backends tardan hasta
~5s en aparecer como sanos en Consul.

## Probar

**Desde el navegador:**
```
http://localhost:3000
```

**Desde la terminal:**
```bash
curl -X POST http://localhost:8080/scan \
  -H "Content-Type: application/json" \
  -d '{"target":"192.168.1.20","modules":["port_scan","http_scan","tls_scan"]}'
```

**Ver el balanceo de nginx entre los middlewares** (revisa el header
`X-Upstream`; la IP debe alternar entre dos valores):
```bash
for i in 1 2 3 4; do
  curl -si -X POST http://localhost:8080/scan \
    -d '{"target":"192.168.1.20","modules":["port_scan"]}' | grep X-Upstream
done
```

**Ver qué middleware atendió cada petición:**
```bash
docker-compose logs middleware-1 middleware-2 | grep dispatch
```

**Probar la tolerancia a fallos de nginx** (apaga una réplica y sigue
respondiendo con la otra). Con una réplica caída, todas las respuestas
deben ser `200`:
```bash
docker-compose stop middleware-2
for i in 1 2 3 4 5 6; do
  curl -s -o /dev/null -w "%{http_code} " -X POST http://localhost:8080/scan \
    -d '{"target":"192.168.1.20","modules":["port_scan"]}'
done; echo
docker-compose start middleware-2
```
(Si tu instalación usa el binario standalone como este proyecto, escribe `docker-compose` con
guion en lugar de `docker compose`.)

**Ver los resultados guardados en la base de datos:**
```bash
docker-compose exec db psql -U postgres -d monolito -c "SELECT * FROM scan_results;"
```

## Probar Consul

**Ver los servicios registrados** (interfaz web o API):
```
http://localhost:8500/ui
```
```bash
curl -s "http://localhost:8500/v1/health/service/port_scan?passing=true" | grep -o 'port-scan-[0-9]' | sort -u
```
Deben aparecer `port-scan-1` y `port-scan-2`.

**Baja del registry** — al detener un backend se da de baja y el middleware
deja de usarlo:
```bash
docker-compose stop port-scan-2
docker-compose logs port-scan-2 | tail -2        # "dado de baja en Consul: port-scan-2"
curl -s "http://localhost:8500/v1/health/service/port_scan?passing=true" | grep -o 'port-scan-[0-9]' | sort -u
```
Ahora solo debe aparecer `port-scan-1`, y los escaneos (`port_scan`) siguen
respondiendo `200`.

**Alta del registry** — al volver a levantarlo se registra solo:
```bash
docker-compose start port-scan-2
```
Tras unos segundos vuelve a aparecer en Consul y en el reparto del middleware:
```bash
docker-compose logs middleware-1 middleware-2 | grep "dispatch: port_scan"
```

## Resultados de las pruebas del gateway

**1. Reparto round robin** — el header `X-Upstream` alterna entre las IPs
internas de `middleware-1` y `middleware-2`:
```
X-Upstream: 172.18.0.7:8080
X-Upstream: 172.18.0.8:8080
X-Upstream: 172.18.0.7:8080
X-Upstream: 172.18.0.8:8080
```

**2. Una réplica caída (`middleware-2` detenido)** — cuando a nginx le toca
la réplica caída, falla la conexión y reintenta con la otra. El header
lista ambos intentos y la **última IP** es la que respondió:
```
X-Upstream: 172.18.0.7:8080
X-Upstream: 172.18.0.8:8080, 172.18.0.7:8080
X-Upstream: 172.18.0.7:8080
X-Upstream: 172.18.0.8:8080, 172.18.0.7:8080
```
Las IPs dobles siguen apareciendo hasta que se acumulan `max_fails=3`
fallos; entonces nginx marca la réplica como caída durante `fail_timeout`
(30s) y deja de intentarla.

**3. Visto desde el cliente** — con una réplica caída, el servicio no se
interrumpe:
```
200 200 200 200 200 200
```

## Apagar

```bash
docker-compose down          # detiene y elimina contenedores (conserva los datos)
docker-compose down -v       # además borra el volumen de la base de datos
```

## Patrones de diseño usados en el backend

| Servicio | Patrón | Dónde |
|---|---|---|
| `port-scan` | Repository | `repository.go` — interfaz `ResultRepository` + `PostgresRepository` |
| `http-scan` | Strategy | `strategy.go` — interfaz `CheckStrategy` con verificaciones intercambiables |
| `tls-scan` | Factory | `factory.go` — función `NewChecker(kind)` que construye el checker correcto |

## Limitaciones conocidas

- El escaneo es simulado, no real (ver nota al inicio).
- Sin autenticación en ningún endpoint, y el gRPC interno va sin TLS
  (`insecure`); los puertos 9001–9004 publicados al host son solo para pruebas.
- El health check de nginx es pasivo (reacciona a fallos reales de
  peticiones); el activo lo hace Consul con los backends.
- Las URLs de los middlewares están fijas en `nginx.conf` (el discovery
  dinámico con Consul solo aplica a los backends, no a nginx).
- Consul corre en modo `-dev` (un nodo, en memoria): si se reinicia el
  contenedor `consul`, el registry se pierde y hay que reiniciar los
  backends para que se registren de nuevo.
- El middleware consulta a Consul en cada petición (sin caché): si Consul
  no responde, los escaneos regresan `503`.
- Configuración (DSN de Postgres, URL del middleware) hardcodeada en el
  código, no por variables de entorno.
- Sin reintentos automáticos si una instancia falla a mitad de una
  petición (aunque Consul la detecta como caída en su siguiente health check,
  hasta ~5s después).
- Kafka corre con un solo nodo y replicación 1 (suficiente para la práctica; en
  producción serían 3 brokers y replicación 3). Los resultados de `/jobs` se
  guardan en memoria del middleware (se pierden al reiniciarlo; la copia
  durable está en Postgres) y no hay cola de errores (dead-letter) ni reintentos.
