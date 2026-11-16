package domain

import (
	"errors"
	"fmt"
	"time"
)

// Un incidente es el síntoma; un problema es la causa de uno o más incidentes.

// Ciclo de vida del incidente (ITIL 4), en tres etapas:
//   - Abierto (resolvedAt vacío): registro y diagnóstico, por los estados configurables del flujo.
//   - Resuelto (resolvedAt, sin closedAt): el último estado del flujo, con código y nota. Si el servicio
//     no quedó restablecido, quien gestiona lo reabre.
//   - Cerrado (closedAt): lo cierra quien gestiona o se cierra solo tras AutoCloseDays sin actividad.
//     Es de solo lectura: si vuelve a pasar, se registra un incidente relacionado.
//
// Cada paso queda en el historial del incidente.
//
// El mismo ticket sirve para las solicitudes de servicio (Kind == KindRequest): un pedido estándar del
// catálogo (un alta, un acceso, un equipo), no una falla. Comparten el ciclo de vida, pero una solicitud no
// tiene servicio, prioridad ni problema: su SLA sale del ítem del catálogo (RequestType) y se resuelve con
// sus propios códigos (ver ResolutionCodesFor).
type Incident struct {
	ID int `json:"id"`
	// Kind: KindIncident o KindRequest. Los tickets guardados antes de las solicitudes son incidentes (ver Tenant.Normalize).
	Kind string `json:"kind"`
	// RequestType: el ítem del catálogo de una solicitud (Config.RequestTypes); "" en un incidente.
	RequestType string `json:"requestType"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Service     string `json:"service"`
	Priority    string `json:"priority"`
	// Impacto y urgencia de los que sale la prioridad (ver PriorityFor). "" en los registrados con la prioridad
	// elegida a mano (los anteriores a la matriz, o por la API sin impacto ni urgencia).
	Impact     string            `json:"impact"`
	Urgency    string            `json:"urgency"`
	Status     string            `json:"status"` // uno de Config.States
	Fields     map[string]string `json:"fields"`
	CreatedBy  int               `json:"createdBy"`
	AssigneeID int               `json:"assigneeId"` // 0 = sin asignar
	ProblemID  int               `json:"problemId"`  // 0 = sin problema
	CreatedAt  time.Time         `json:"createdAt"`
	DueAt      *time.Time        `json:"dueAt,omitempty"` // vencimiento del SLA, si el módulo está activo
	ResolvedAt *time.Time        `json:"resolvedAt,omitempty"`
	History    []Event           `json:"history"`
	// Vencimiento de la primera respuesta (si el módulo SLA está activo; se fija al registrar y no se mueve) y cuándo llegó.
	ResponseDueAt   *time.Time `json:"responseDueAt,omitempty"`
	FirstResponseAt *time.Time `json:"firstResponseAt,omitempty"`
	// Si está, el reloj del SLA de resolución está detenido desde ese momento (ver SyncPause).
	PausedAt       *time.Time `json:"pausedAt,omitempty"`
	ResolutionCode string     `json:"resolutionCode"` // uno de ResolutionCodes; "" mientras está abierto
	ResolutionNote string     `json:"resolutionNote"`
	ReopenCount    int        `json:"reopenCount"`
	ClosedAt       *time.Time `json:"closedAt,omitempty"`
	// AutoCloseHold: quien lo reportó respondió estando resuelto; ya no se cierra solo (ver Tenant.AutoClose).
	AutoCloseHold     bool `json:"autoCloseHold"`
	RelatedIncidentID int  `json:"relatedIncidentId"` // incidente cerrado del que es continuación; 0 = ninguno
	// Archivos adjuntos (ver Attachment). Los ve cualquiera que vea el incidente.
	Attachments []Attachment `json:"attachments"`
	// Valoración de quien lo reportó, una vez resuelto: de 1 (muy mal) a 5 (excelente); 0 = sin valorar.
	// No es un evento del historial: valorar no es actividad y no frena el cierre automático.
	Rating        int        `json:"rating"`
	RatingComment string     `json:"ratingComment"`
	RatedAt       *time.Time `json:"ratedAt,omitempty"`
}

// Satisfied: la valoración cuenta como conforme (4 o 5), como en una encuesta CSAT.
func (inc *Incident) Satisfied() bool { return inc.Rating >= 4 }

// ErrClosed: un incidente cerrado es de solo lectura.
var ErrClosed = errors.New("el incidente está cerrado: registrá un incidente relacionado")

// Resolved: está en la etapa Resuelto (resuelto y todavía sin cerrar).
func (inc *Incident) Resolved() bool { return inc.ResolvedAt != nil && inc.ClosedAt == nil }

// Closed: está en la etapa Cerrado.
func (inc *Incident) Closed() bool { return inc.ClosedAt != nil }

// Tipos de ticket.
const (
	KindIncident = "incidente" // algo dejó de funcionar
	KindRequest  = "solicitud" // un pedido estándar del catálogo
)

// IsRequest: es una solicitud de servicio.
func (inc *Incident) IsRequest() bool { return inc.Kind == KindRequest }

// Eventos del historial de un incidente.
const (
	EvCreated  = "creado"      // registro
	EvStatus   = "estado"      // "De X a Y"
	EvAssigned = "asignado"    // "Asignado a Nombre" | "Sin asignar"
	EvProblem  = "problema"    // vinculado o desvinculado de un problema
	EvReply    = "respuesta"   // comunicación con quien reportó: la ven todos
	EvNote     = "nota"        // nota interna de trabajo: no la ve quien solo reporta
	EvResolved = "resuelto"    // resolución: "De X a Y · etiqueta del código: nota"
	EvReopened = "reabierto"   // reapertura: "De X a Y: motivo"
	EvClosed   = "cerrado"     // cierre, a mano o automático (userId 0)
	EvRelated  = "relacionado" // en un cerrado: se registró un incidente relacionado
	EvAttached = "adjunto"     // se agregó o se quitó un archivo adjunto
)

// ClosingCodes: los códigos con los que resolver también cierra (no hay nada que confirmar).
var ClosingCodes = []string{"duplicado", "cancelado"}

// ResolutionCodes: cómo terminó el incidente. Lista fija: es lo que el producto propone.
var ResolutionCodes = []string{"solucionado", "workaround", "duplicado", "sin_reproducir", "cancelado"}

// RequestResolutionCodes: cómo terminó una solicitud. Una solicitud no se "soluciona": se cumple o se rechaza.
var RequestResolutionCodes = []string{"cumplida", "rechazada", "duplicado", "cancelado"}

// ResolutionCodesFor: los códigos con los que se resuelve un ticket de ese tipo.
func ResolutionCodesFor(kind string) []string {
	if kind == KindRequest {
		return RequestResolutionCodes
	}
	return ResolutionCodes
}

// ResolutionLabels: cómo se lee cada código en el historial (resolutionCode guarda el código).
var ResolutionLabels = map[string]string{
	"solucionado":    "Solucionado",
	"workaround":     "Con workaround",
	"duplicado":      "Duplicado",
	"sin_reproducir": "No se pudo reproducir",
	"cancelado":      "Cancelado",
	"cumplida":       "Cumplida",
	"rechazada":      "Rechazada",
}

// Log agrega un evento al historial del incidente. Recibe el momento para que coincida con
// los cambios que registra (primera respuesta, pausa, resolución).
func (inc *Incident) Log(at time.Time, by int, kind, format string, a ...any) {
	inc.History = append(inc.History, Event{at, kind, fmt.Sprintf(format, a...), by})
}

// AutoClose cierra el incidente si está resuelto y lleva days o más sin actividad, salvo que quien lo reportó
// lo haya retenido respondiendo (AutoCloseHold). Actividad es cualquier evento del historial, así que cada
// evento nuevo reinicia la cuenta, que arranca como mínimo en la resolución (los incidentes de datos viejos
// no tienen historial). El cierre queda con la fecha en que correspondía, no con la del barrido.
func (inc *Incident) AutoClose(days int, now time.Time) bool {
	if !inc.Resolved() || inc.AutoCloseHold {
		return false
	}
	active := *inc.ResolvedAt
	if k := len(inc.History); k > 0 && inc.History[k-1].At.After(active) {
		active = inc.History[k-1].At
	}
	at := active.Add(time.Duration(days) * 24 * time.Hour)
	if at.After(now) {
		return false
	}
	inc.ClosedAt = &at
	inc.Log(at, 0, EvClosed, "Cerrado automáticamente tras %d días sin actividad", days)
	return true
}

// Respond registra la primera respuesta. Se fija una sola vez.
func (inc *Incident) Respond(at time.Time) {
	if inc.FirstResponseAt == nil {
		inc.FirstResponseAt = &at
	}
}

// SyncPause detiene o reanuda el reloj del SLA de resolución según el estado actual. Solo aplica si el
// incidente tiene SLA. Al reanudar corre dueAt lo que duró la pausa: así el vencimiento guardado ya la descuenta.
// La pausa nunca toca el vencimiento de la primera respuesta, que se fija al registrar y no se mueve.
func (inc *Incident) SyncPause(pauseStates []string, at time.Time) {
	if inc.DueAt == nil {
		return
	}
	paused := Has(pauseStates, inc.Status)
	switch {
	case paused && inc.PausedAt == nil:
		inc.PausedAt = &at
	case !paused && inc.PausedAt != nil:
		due := inc.DueAt.Add(at.Sub(*inc.PausedAt))
		inc.DueAt, inc.PausedAt = &due, nil
	}
}

type NewIncident struct {
	// Kind: KindIncident (o vacío) o KindRequest. Una solicitud pide RequestType (un ítem del catálogo) e ignora
	// el servicio, la prioridad, el impacto, la urgencia y el problema.
	Kind        string `json:"kind"`
	RequestType string `json:"requestType"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Service     string `json:"service"`
	// Con impacto y urgencia, la prioridad se calcula con la matriz y Priority se ignora.
	// Sin ninguno de los dos, se usa Priority tal cual (compatibilidad con los clientes anteriores).
	Impact     string            `json:"impact"`
	Urgency    string            `json:"urgency"`
	Priority   string            `json:"priority"`
	Fields     map[string]string `json:"fields"`
	AssigneeID int               `json:"assigneeId"`
	ProblemID  int               `json:"problemId"`
	// RelatedIncidentID: incidente cerrado del que este es continuación (0 = ninguno).
	RelatedIncidentID int `json:"relatedIncidentId"`
}

// IncidentPatch: solo se aplican los campos presentes.
// El código y la nota de resolución solo se usan al pasar al estado de resolución, y ahí son obligatorios.
type IncidentPatch struct {
	Status         *string `json:"status"`
	AssigneeID     *int    `json:"assigneeId"`
	ProblemID      *int    `json:"problemId"`
	ResolutionCode string  `json:"resolutionCode"`
	ResolutionNote string  `json:"resolutionNote"`
}

// Ciclo de vida de un problema: fijo, es el proceso que propone el producto.
const (
	Identificado  = "identificado"
	EnAnalisis    = "en_analisis"
	ErrorConocido = "error_conocido" // causa raíz conocida, sin solución definitiva
	Resuelto      = "resuelto"
)

type Event struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"` // problema: "" | analisis | conocido | resuelto | comentario · incidente: ver EvCreated
	Text   string    `json:"text"`
	UserID int       `json:"userId"`
}

type Problem struct {
	ID          int       `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Service     string    `json:"service"`
	Status      string    `json:"status"`
	OwnerID     int       `json:"ownerId"`
	RootCause   string    `json:"rootCause"`
	Workaround  string    `json:"workaround"`
	Solution    string    `json:"solution"`
	CreatedAt   time.Time `json:"createdAt"`
	History     []Event   `json:"history"`
	// WorkaroundShared: quienes reportan ven el workaround mientras escriben un incidente parecido,
	// y pueden resolverlo solos sin abrir un ticket (ver Deflections).
	WorkaroundShared bool            `json:"workaroundShared"`
	WorkaroundUses   []WorkaroundUse `json:"workaroundUses"`
	// Deflections: veces que a alguien le alcanzó el workaround compartido y no abrió el ticket.
	Deflections int `json:"deflections"`
	// Review: la revisión post-problema, sin culpables, una vez resuelto (ver Review).
	Review *Review `json:"review,omitempty"`
	Swarm  *Swarm  `json:"swarm,omitempty"`
	// Preloaded: error conocido típico del rubro, cargado en el onboarding (ver Template.KnownErrors).
	Preloaded bool `json:"preloaded"`
}

// Review: revisión post-problema sin culpables. Cuenta qué pasó y qué se aprendió, y deja acciones con
// responsable y fecha para que no vuelva a pasar algo parecido. Los números (impacto, tiempos) los calcula
// la aplicación a partir del historial: acá va lo que solo puede contar el equipo.
type Review struct {
	WhatHappened string         `json:"whatHappened"`
	Learned      string         `json:"learned"`
	Actions      []ReviewAction `json:"actions"`
	At           time.Time      `json:"at"`
	UserID       int            `json:"userId"`
}

type ReviewAction struct {
	Text    string `json:"text"`
	OwnerID int    `json:"ownerId"` // 0 = sin responsable
	Due     string `json:"due"`     // AAAA-MM-DD; vacío = sin fecha
	Done    bool   `json:"done"`
}

// WorkaroundUse: una vez que un incidente se resolvió con el workaround del problema.
// Failed: el incidente se reabrió después, así que el workaround no alcanzó.
type WorkaroundUse struct {
	IncidentID int       `json:"incidentId"`
	At         time.Time `json:"at"`
	UserID     int       `json:"userId"`
	Failed     bool      `json:"failed"`
}

// WorkaroundEffectiveness: qué parte de los usos del workaround no terminó en reapertura (0 a 100).
// ok es false si todavía no se usó.
func (p *Problem) WorkaroundEffectiveness() (pct int, ok bool) {
	if len(p.WorkaroundUses) == 0 {
		return 0, false
	}
	good := 0
	for _, u := range p.WorkaroundUses {
		if !u.Failed {
			good++
		}
	}
	return good * 100 / len(p.WorkaroundUses), true
}

// Log agrega un evento al historial del problema.
func (p *Problem) Log(by int, kind, format string, a ...any) {
	p.History = append(p.History, Event{time.Now(), kind, fmt.Sprintf(format, a...), by})
}

type NewProblem struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Service     string `json:"service"`
	IncidentIDs []int  `json:"incidentIds"`
}

// Advance: lo que cada etapa del problema exige para pasar a la siguiente.
type Advance struct {
	RootCause  string `json:"rootCause"`
	Workaround string `json:"workaround"`
	Solution   string `json:"solution"`
}

// Suggestion: incidentes recurrentes de un mismo servicio que nadie investigó todavía.
type Suggestion struct {
	Service     string `json:"service"`
	IncidentIDs []int  `json:"incidentIds"`
}
