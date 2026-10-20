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

Un `data.json` de una versión anterior se carga igual: a los incidentes sin historial se les pone `history: []`
(no se inventa), las cuentas sin `responseHours` reciben las de la plantilla "otro", sin `pauseStates` quedan con `[]`
y sin `autoCloseDays` (o con 0, que no es válido) arrancan con 2. Los incidentes ya resueltos sin código quedan con
`resolutionCode: ""`. Al cargar se hace el barrido de cierre automático y, si cerró algo, se guarda.

## Ciclo de vida del incidente

Sigue la gestión de incidentes de ITIL 4, en tres etapas:

| Etapa | Cómo se reconoce | Qué pasa |
|---|---|---|
| **Abierto** | `resolvedAt` vacío | **registro** (estado inicial) y **diagnóstico** (estados intermedios del flujo configurable) |
| **Resuelto** | `resolvedAt`, sin `closedAt` | **resolución**: el último estado del flujo, con `resolutionCode` y `resolutionNote` obligatorios. Si el servicio no quedó restablecido, quien gestiona lo **reabre** |
| **Cerrado** | `closedAt` | **cierre**: a mano o automático tras `autoCloseDays` sin actividad. De solo lectura: si vuelve a pasar, se registra un incidente relacionado (`relatedIncidentId`) |

El cierre no es un estado del flujo: es una etapa aparte. Solo quien tiene `resolver` cambia el estado, resuelve,
reabre y cierra; quien solo reporta publica respuestas en sus incidentes.

- Códigos de resolución (en el historial, su etiqueta): `solucionado` (Solucionado), `workaround` (Con workaround), `duplicado` (Duplicado), `sin_reproducir` (No se pudo reproducir), `cancelado` (Cancelado). `resolutionCode` guarda el código. `duplicado` y `cancelado` resuelven y cierran en el mismo momento; el resto queda resuelto. Un resuelto no cambia de estado: hay que reabrirlo.
- **Reapertura** (solo un resuelto): vuelve al estado inicial, borra la resolución, suma `reopenCount` y libera `autoCloseHold`. Si su asignado ya no tiene `resolver` o ya no existe, queda sin asignar (evento `asignado` después del `reabierto`). El `dueAt` original se mantiene (cuenta contra el SLA).
- **Cierre automático**: un resuelto se cierra solo tras `config.autoCloseDays` días sin actividad (2 por defecto, mínimo 1). Actividad es cualquier evento del historial, incluida la resolución, así que cada evento nuevo reinicia la cuenta: `closedAt` = último evento + esos días, con el evento `cerrado` sin usuario (`userId: 0`, `Cerrado automáticamente tras N días sin actividad`). Si quien lo reportó responde estando resuelto, queda `autoCloseHold: true` y ya no se cierra solo hasta que quien gestiona lo cierre o lo reabra. El barrido es perezoso: al cargar el archivo y al principio de cada pedido autenticado.
- **Primera respuesta** (`firstResponseAt`): la primera respuesta pública de alguien con `resolver`. Cambiar el estado, asignar o resolver un problema no cuentan. Con el módulo SLA, el registro calcula `responseDueAt` con `config.responseHours`.
- **Pausa del SLA**: en los estados de `config.pauseStates` el reloj de resolución se detiene (`pausedAt`). Al salir, `dueAt` se corre lo que duró la pausa: el vencimiento guardado ya la descuenta. La pausa nunca mueve `responseDueAt`, que se fija al registrar el incidente.
- Resolver un problema resuelve (sin cerrar) sus incidentes abiertos con código `solucionado` y solo la solución como nota (sin nombrar al problema: quien solo reporta ve la resolución); los cerrados no cambian. Un cerrado solo se vincula a un problema al crear el problema (`incidentIds`, por ejemplo desde una sugerencia): cambia su `problemId` y queda el evento en su historial.

Cada paso queda en el `history` del incidente:

| `kind` | Cuándo |
|---|---|
| `creado` | al registrarlo (`Incidente registrado`, o `(relacionado con #n)`) |
| `estado` | en cada cambio de estado del diagnóstico (`De X a Y`) |
| `asignado` | al cambiar el asignado (`Asignado a Nombre` / `Sin asignar`), o al reabrirlo si su asignado ya no tiene `resolver` (`Sin asignar (Nombre ya no atiende incidentes)`) o ya no existe (`Sin asignar (el usuario asignado ya no existe)`) |
| `problema` | al vincularlo o desvincularlo de un problema (en un cerrado, solo al crear el problema): no lo ve quien solo reporta |
| `respuesta` | comunicación con quien lo reportó: la ven todos |
| `nota` | nota interna de trabajo: no la ve quien solo reporta |
| `resuelto` | resolución: `De X a Y · Etiqueta: nota` con la etiqueta del código, por ejemplo `De En curso a Resuelto · No se pudo reproducir: …` (un solo evento) |
| `reabierto` | reapertura: `De X a Y: motivo` (un solo evento) |
| `cerrado` | cierre: `Cerrado por Nombre`, o automático con `userId: 0` (`Cerrado automáticamente tras N días sin actividad`) |
| `relacionado` | en un cerrado, al registrar un incidente que lo continúa (`Se registró #m relacionado`) |

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

Las dos cuentas de demo suman al flujo de su plantilla el estado "Esperando al solicitante", justo antes del de
resolución, como único estado de pausa del SLA (las plantillas no traen estados de pausa).

## API

Salvo alta, login y plantillas, todo requiere `Authorization: Bearer <token>` y responde con el estado completo de la cuenta, recortado según los permisos del usuario (quien solo ve sus incidentes no recibe las notas internas ni los eventos `problema`).

| Endpoint | Qué hace | Permiso |
|---|---|---|
| `POST /api/signup` | contrata: crea la cuenta y su titular | — |
| `POST /api/login` · `POST /api/logout` | abre y cierra sesión | — |
| `GET /api/templates` | plantillas de rubro para el onboarding | — |
| `GET /api/state` | estado de la cuenta | sesión |
| `POST /api/onboarding` | aplica las respuestas del onboarding: rubro, servicios y cómo trabaja el cliente (equipo, plazos, recurrencia, datos propios). Repetible hasta terminar | `config` |
| `POST /api/onboarding/finish` | da por terminado el onboarding | `config` |
| `PUT /api/config` | módulos, servicios, estados, campos, SLA (resolución y primera respuesta), estados de pausa, días para el cierre automático y regla de recurrencia | `config` |
| `POST /api/users` · `PUT/DELETE /api/users/{id}` | hasta 5 usuarios además del titular. Un cambio de rol que le saca `resolver` a quien tiene incidentes abiertos asignados, o `problemas` a quien es responsable de un problema activo (no resuelto), se rechaza: hay que reasignarlos primero | `config` |
| `POST /api/roles` · `PUT/DELETE /api/roles/{id}` | roles con permisos a medida. Un rol no pierde `resolver` mientras alguno de sus usuarios tenga incidentes abiertos asignados, ni `problemas` mientras alguno sea responsable de un problema activo | `config` |
| `POST /api/incidents` | registra un incidente. `assigneeId` tiene que ser alguien con `resolver` (0 = sin asignar). `relatedIncidentId` lo relaciona con uno cerrado (quien solo reporta, con uno suyo) | `crear` |
| `POST /api/incidents/{id}` | cambia estado, asignado o problema. El asignado tiene que tener `resolver` (`assigneeId: 0` lo deja sin asignar) y `problemId: 0` lo desvincula. Para resolverlo (pasar al último estado) exige `resolutionCode` y `resolutionNote`. Un cerrado no se modifica | `resolver` |
| `POST /api/incidents/{id}/comments` | `{text, public}`: respuesta (`public: true`) o nota interna. Quien lo reportó solo publica respuestas en los suyos | `resolver`, o quien lo reportó |
| `POST /api/incidents/{id}/reopen` | `{reason}`: reabre un incidente resuelto (no uno cerrado) | `resolver` |
| `POST /api/incidents/{id}/close` | `{}`: cierra un incidente resuelto | `resolver` |
| `POST /api/problems` · `/{id}/advance` · `/{id}/owner` · `/{id}/comments` | gestión de problemas. Crear un problema con `incidentIds` vincula esos incidentes, también los cerrados. El responsable (`ownerId`) tiene que tener `problemas` | `problemas` |

Permisos: `panel`, `crear`, `resolver`, `problemas`, `config`. Un 403 es "no es tuyo" o "tu rol no puede"; pedir algo
que la etapa del incidente no permite (por ejemplo, reabrir uno cerrado) es un 400.
