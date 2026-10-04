# TaaS backend

API en Go (solo stdlib, Go 1.24+) de TaaS: ticketera configurable como servicio, con gestión de problemas (ITIL 4).

Cada cliente contrata una cuenta, la configura en el onboarding (rubro, servicios y cómo trabaja) y define sus usuarios y roles.

## Estructura

```
cmd/api/              punto de entrada: arma repositorios -> servicios -> handlers
internal/
  domain/             entidades y sus reglas propias (Tenant, Config, Incident, Problem, permisos, errores)
  repository/         interfaces de persistencia + implementaciones (cuentas en JSON, sesiones en memoria)
  service/            casos de uso: auth, usuarios y roles, onboarding y configuración, incidentes, problemas
  handler/            capa HTTP: rutas, middleware, decodificar el pedido y armar la respuesta
  seed/               cuentas de demo
```

Las dependencias van en un solo sentido: `handler -> service -> repository -> domain`.
Para cambiar el almacenamiento (por ejemplo a MySQL) alcanza con otra implementación de `repository.Tenants`.

## Correr

```bash
go run ./cmd/api            # http://localhost:8080
go run ./cmd/api -reset     # descarta data.json y vuelve a las cuentas de demo
go test ./...
```

Con Docker:

```bash
docker build -t taas-backend .
docker run -p 8080:8080 -v taas-data:/data taas-backend
```

Los datos se guardan en `data.json` (se crea solo; incluye hashes de contraseñas, está en `.gitignore`). En Docker viven en el volumen `/data`.
Las sesiones viven en memoria: al reiniciar el servidor hay que volver a entrar.

## Cuentas de demo

Todos los usuarios de demo usan la contraseña definida en `DemoPassword` (`internal/seed/seed.go`).

| Cuenta | Usuario | Rol |
|---|---|---|
| Nexo Soporte IT | `ana@nexo.demo` | Administrador (titular) |
| | `martin@nexo.demo`, `lucia@nexo.demo` | Agente |
| | `sofia@nexo.demo` | Solicitante (solo crea tickets y ve los suyos) |
| Clínica del Sol | `carla@clinicadelsol.demo` | Administrador (titular) |
| | `diego@clinicadelsol.demo`, `paula@clinicadelsol.demo` | Agente |
| | `tomas@clinicadelsol.demo` | Solicitante |

## API

Salvo alta, login y plantillas, todo requiere `Authorization: Bearer <token>` y responde con el estado completo de la cuenta, recortado según los permisos del usuario.

| Endpoint | Qué hace | Permiso |
|---|---|---|
| `POST /api/signup` | contrata: crea la cuenta y su titular | — |
| `POST /api/login` · `POST /api/logout` | abre y cierra sesión | — |
| `GET /api/templates` | plantillas de rubro para el onboarding | — |
| `GET /api/state` | estado de la cuenta | sesión |
| `POST /api/onboarding` | aplica las respuestas del onboarding: rubro, servicios y cómo trabaja el cliente (equipo, plazos, recurrencia, datos propios). Repetible hasta terminar | `config` |
| `POST /api/onboarding/finish` | da por terminado el onboarding | `config` |
| `PUT /api/config` | módulos, servicios, estados, campos, SLA y regla de recurrencia | `config` |
| `POST /api/users` · `PUT/DELETE /api/users/{id}` | hasta 5 usuarios además del titular | `config` |
| `POST /api/roles` · `PUT/DELETE /api/roles/{id}` | roles con permisos a medida | `config` |
| `POST /api/incidents` | crea un incidente | `crear` |
| `POST /api/incidents/{id}` | cambia estado, asignado o problema | `resolver` |
| `POST /api/problems` · `/{id}/advance` · `/{id}/owner` · `/{id}/comments` | gestión de problemas | `problemas` |

Permisos: `panel`, `crear`, `resolver`, `problemas`, `config`.
