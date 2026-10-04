package domain

import (
	"fmt"
	"time"
)

// Un incidente es el síntoma; un problema es la causa de uno o más incidentes.

type Incident struct {
	ID          int               `json:"id"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Service     string            `json:"service"`
	Priority    string            `json:"priority"`
	Status      string            `json:"status"` // uno de Config.States
	Fields      map[string]string `json:"fields"`
	CreatedBy   int               `json:"createdBy"`
	AssigneeID  int               `json:"assigneeId"` // 0 = sin asignar
	ProblemID   int               `json:"problemId"`  // 0 = sin problema
	CreatedAt   time.Time         `json:"createdAt"`
	DueAt       *time.Time        `json:"dueAt,omitempty"` // vencimiento del SLA, si el módulo está activo
	ResolvedAt  *time.Time        `json:"resolvedAt,omitempty"`
}

type NewIncident struct {
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Service     string            `json:"service"`
	Priority    string            `json:"priority"`
	Fields      map[string]string `json:"fields"`
	AssigneeID  int               `json:"assigneeId"`
	ProblemID   int               `json:"problemId"`
}

// IncidentPatch: solo se aplican los campos presentes.
type IncidentPatch struct {
	Status     *string `json:"status"`
	AssigneeID *int    `json:"assigneeId"`
	ProblemID  *int    `json:"problemId"`
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
	Kind   string    `json:"kind"` // "" | analisis | conocido | resuelto | comentario
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
