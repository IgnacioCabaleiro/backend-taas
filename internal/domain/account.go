// Package domain define las entidades de TaaS y sus reglas propias, sin HTTP ni almacenamiento.
package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Errores que la capa HTTP traduce a un código de estado. Cualquier otro error es de validación (400).
var (
	ErrNotFound     = errors.New("no encontrado")
	ErrForbidden    = errors.New("tu rol no permite hacer esto")
	ErrUnauthorized = errors.New("iniciá sesión para continuar")
	// ErrInternal: responde 500 sin el detalle.
	ErrInternal = errors.New("Servicio no disponible")
)

// Denied es un ErrForbidden (403) con un motivo propio, para cuando no alcanza con "tu rol no permite hacer esto".
func Denied(reason string) error { return denied(reason) }

type denied string

func (d denied) Error() string        { return string(d) }
func (d denied) Is(target error) bool { return target == ErrForbidden }

// Permisos: lo que el titular decide que cada rol puede ver y hacer.
const (
	PermPanel    = "panel"     // ver el panel
	PermCreate   = "crear"     // crear tickets
	PermResolve  = "resolver"  // atender y resolver tickets
	PermProblems = "problemas" // gestionar problemas y errores conocidos
	PermConfig   = "config"    // configurar la cuenta, sus usuarios y roles
)

var AllPerms = []string{PermPanel, PermCreate, PermResolve, PermProblems, PermConfig}

// MaxUsers: usuarios que el titular puede crear además del suyo.
const MaxUsers = 5

type Role struct {
	ID     int      `json:"id"`
	Name   string   `json:"name"`
	Perms  []string `json:"perms"`
	Locked bool     `json:"locked"` // el rol del titular: no se edita ni se borra
}

type User struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	RoleID int    `json:"roleId"`
	Hash   string `json:"hash,omitempty"` // nunca sale por la API (ver service.State)
}

// Tenant: la cuenta de un cliente. Todo lo que hay adentro es solo suyo.
type Tenant struct {
	ID         int         `json:"id"`
	Name       string      `json:"name"`
	Industry   string      `json:"industry"`
	Onboarded  bool        `json:"onboarded"`
	OwnerID    int         `json:"ownerId"` // quien contrató
	Config     Config      `json:"config"`
	Roles      []Role      `json:"roles"`
	Users      []User      `json:"users"`
	NextUserID int         `json:"nextUserId"`
	NextRoleID int         `json:"nextRoleId"`
	Incidents  []*Incident `json:"incidents"`
	Problems   []*Problem  `json:"problems"`
}

func (t *Tenant) Clone() *Tenant {
	data, err := json.Marshal(t)
	c := &Tenant{}
	if err == nil {
		err = json.Unmarshal(data, c)
	}
	if err != nil {
		panic(fmt.Sprintf("clonar la cuenta %d: %v", t.ID, err))
	}
	return c
}

// Normalize completa una cuenta guardada antes de que existiera el ciclo de vida del incidente
// (lo llama repository.JSONTenants al cargar).
func (t *Tenant) Normalize() {
	c := &t.Config
	if c.ResponseHours == nil {
		c.ResponseHours = map[string]int{}
		for k, v := range DefaultResponseHours {
			c.ResponseHours[k] = v
		}
	}
	if c.PauseStates == nil {
		c.PauseStates = []string{}
	}
	if c.AutoCloseDays == 0 { // ausente: 0 no es un valor válido (ver Config.Validate)
		c.AutoCloseDays = DefaultAutoCloseDays
	}
	for _, inc := range t.Incidents {
		if inc.History == nil {
			inc.History = []Event{} // los incidentes viejos no tienen historial: no se inventa
		}
	}
}

// AutoClose hace el barrido de cierre automático de la cuenta (ver Incident.AutoClose) y devuelve cuántos cerró.
// Se llama de forma perezosa: al cargar la cuenta y al principio de cada pedido.
func (t *Tenant) AutoClose(now time.Time) int {
	n := 0
	for _, inc := range t.Incidents {
		if inc.AutoClose(t.Config.AutoCloseDays, now) {
			n++
		}
	}
	return n
}

func (t *Tenant) User(id int) *User {
	for i := range t.Users {
		if t.Users[i].ID == id {
			return &t.Users[i]
		}
	}
	return nil
}

func (t *Tenant) Role(id int) *Role {
	for i := range t.Roles {
		if t.Roles[i].ID == id {
			return &t.Roles[i]
		}
	}
	return nil
}

// Can indica si el rol del usuario tiene el permiso.
func (t *Tenant) Can(userID int, perm string) bool {
	u := t.User(userID)
	if u == nil {
		return false
	}
	r := t.Role(u.RoleID)
	return r != nil && Has(r.Perms, perm)
}

func (t *Tenant) Incident(id int) (*Incident, error) {
	if id < 1 || id > len(t.Incidents) {
		return nil, fmt.Errorf("incidente %d: %w", id, ErrNotFound)
	}
	return t.Incidents[id-1], nil
}

func (t *Tenant) Problem(id int) (*Problem, error) {
	if id < 1 || id > len(t.Problems) {
		return nil, fmt.Errorf("problema %d: %w", id, ErrNotFound)
	}
	return t.Problems[id-1], nil
}

// ResolvedState es el estado de resolución: el último del flujo configurado. El cierre no es un estado
// del flujo sino una etapa aparte (ver Incident.ClosedAt).
func (t *Tenant) ResolvedState() string { return t.Config.States[len(t.Config.States)-1] }
