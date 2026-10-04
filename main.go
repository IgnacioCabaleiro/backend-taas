// TaaS — ticketera configurable como servicio, con gestión de problemas (ITIL 4).
// Cada cliente contrata una cuenta (tenant), la configura en el onboarding y define
// sus usuarios y roles. Un incidente es el síntoma; un problema es la causa de uno o más incidentes.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// --- Configuración de la cuenta ---

type Field struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"` // text | number | select
	Options  []string `json:"options"`
	Required bool     `json:"required"`
}

// Modules: qué tan compleja es la gestión de tickets del cliente. Se elige en el onboarding.
type Modules struct {
	SLA      bool `json:"sla"`      // vencimientos por prioridad
	Problems bool `json:"problems"` // gestión de problemas y errores conocidos
}

type Config struct {
	Modules        Modules        `json:"modules"`
	Services       []string       `json:"services"`
	States         []string       `json:"states"`   // estados del incidente, en orden; el último es el de cierre
	SLAHours       map[string]int `json:"slaHours"` // horas para resolver, por prioridad
	Fields         []Field        `json:"fields"`   // campos propios del incidente
	RecurrenceMin  int            `json:"recurrenceMin"`
	RecurrenceDays int            `json:"recurrenceDays"`
}

var priorities = []string{"alta", "media", "baja"}

// --- Datos ---

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

type Event struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"` // "" | analisis | conocido | resuelto | comentario
	Text   string    `json:"text"`
	UserID int       `json:"userId"`
}

// Ciclo de vida de un problema: fijo, es el proceso que propone el producto.
const (
	Identificado  = "identificado"
	EnAnalisis    = "en_analisis"
	ErrorConocido = "error_conocido" // causa raíz conocida, sin solución definitiva
	Resuelto      = "resuelto"
)

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

func (p *Problem) log(by int, kind, format string, a ...any) {
	p.History = append(p.History, Event{time.Now(), kind, fmt.Sprintf(format, a...), by})
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

// Suggestion: incidentes recurrentes de un mismo servicio que nadie investigó todavía.
type Suggestion struct {
	Service     string `json:"service"`
	IncidentIDs []int  `json:"incidentIds"`
}

var (
	errNotFound     = errors.New("no encontrado")
	errForbidden    = errors.New("tu rol no permite hacer esto")
	errUnauthorized = errors.New("iniciá sesión para continuar")
)

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (t *Tenant) incident(id int) (*Incident, error) {
	if id < 1 || id > len(t.Incidents) {
		return nil, fmt.Errorf("incidente %d: %w", id, errNotFound)
	}
	return t.Incidents[id-1], nil
}

func (t *Tenant) problem(id int) (*Problem, error) {
	if id < 1 || id > len(t.Problems) {
		return nil, fmt.Errorf("problema %d: %w", id, errNotFound)
	}
	return t.Problems[id-1], nil
}

// needProblems: permiso + módulo activo.
func (t *Tenant) needProblems(by int) error {
	if !t.Config.Modules.Problems {
		return errors.New("la gestión de problemas no está activada en esta cuenta")
	}
	return t.need(by, PermProblems)
}

func (t *Tenant) closedState() string { return t.Config.States[len(t.Config.States)-1] }

// --- Configuración ---

func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

// clean recorta, descarta vacíos y exige que no haya repetidos.
func clean(list []string, what string) ([]string, error) {
	out := []string{}
	for _, s := range list {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		if has(out, s) {
			return nil, fmt.Errorf("%s repetido: %s", what, s)
		}
		out = append(out, s)
	}
	return out, nil
}

func (c *Config) validate() error {
	var err error
	if c.Services, err = clean(c.Services, "servicio"); err != nil {
		return err
	}
	if len(c.Services) == 0 {
		return errors.New("definí al menos un servicio")
	}
	if c.States, err = clean(c.States, "estado"); err != nil {
		return err
	}
	if len(c.States) < 2 {
		return errors.New("hacen falta al menos dos estados: uno inicial y uno de cierre")
	}
	for _, p := range priorities {
		if c.SLAHours[p] < 1 {
			return fmt.Errorf("el SLA de prioridad %s tiene que ser de al menos 1 hora", p)
		}
	}
	keys := []string{}
	for i := range c.Fields {
		f := &c.Fields[i]
		if f.Label = strings.TrimSpace(f.Label); f.Label == "" {
			return errors.New("hay un campo sin nombre")
		}
		if f.Key == "" {
			f.Key = slug(f.Label)
		}
		if has(keys, f.Key) {
			return fmt.Errorf("campo repetido: %s", f.Label)
		}
		keys = append(keys, f.Key)
		switch f.Type {
		case "text", "number":
			f.Options = []string{}
		case "select":
			if f.Options, err = clean(f.Options, "opción"); err != nil {
				return err
			}
			if len(f.Options) < 2 {
				return fmt.Errorf("el campo %s necesita al menos dos opciones", f.Label)
			}
		default:
			return fmt.Errorf("tipo de campo inválido en %s", f.Label)
		}
	}
	if c.Fields == nil {
		c.Fields = []Field{}
	}
	if c.RecurrenceMin < 2 || c.RecurrenceDays < 1 {
		return errors.New("la regla de recurrencia necesita al menos 2 incidentes y 1 día")
	}
	return nil
}

func renames(old, new []string) map[string]string {
	m := map[string]string{}
	if len(old) == len(new) {
		for i := range old {
			m[old[i]] = new[i]
		}
	}
	return m
}

func (t *Tenant) SetConfig(c Config, by int) error {
	if err := t.need(by, PermConfig); err != nil {
		return err
	}
	if err := c.validate(); err != nil {
		return err
	}
	// Misma cantidad de ítems que antes = se renombraron en el lugar: los datos existentes siguen al nombre nuevo.
	states, services := renames(t.Config.States, c.States), renames(t.Config.Services, c.Services)
	t.Config = c
	for _, p := range t.Problems {
		if n, ok := services[p.Service]; ok {
			p.Service = n
		}
	}
	// Incidentes que quedaron en un estado eliminado: vuelven al inicial (o al de cierre si ya estaban resueltos).
	for _, inc := range t.Incidents {
		if n, ok := services[inc.Service]; ok {
			inc.Service = n
		}
		if n, ok := states[inc.Status]; ok {
			inc.Status = n
		}
		if inc.ResolvedAt != nil {
			inc.Status = t.closedState()
		} else if !has(c.States[:len(c.States)-1], inc.Status) {
			inc.Status = c.States[0]
		}
	}
	return nil
}

// Onboard aplica las respuestas del onboarding. Se puede repetir (el titular puede volver atrás)
// hasta que lo da por terminado con FinishOnboarding.
func (t *Tenant) Onboard(in Setup, by int) error {
	if err := t.need(by, PermConfig); err != nil {
		return err
	}
	if t.Onboarded {
		return errors.New("la cuenta ya está configurada: los ajustes se hacen desde Configuración")
	}
	tpl, ok := templates[in.Template]
	if !ok {
		return errors.New("elegí un rubro")
	}
	c, err := tpl.build(in)
	if err != nil {
		return err
	}
	t.Industry = tpl.Industry
	if in.Template == "otro" {
		if t.Industry = strings.TrimSpace(in.Industry); t.Industry == "" {
			return errors.New("contanos a qué se dedica tu empresa")
		}
	}
	t.Config = c
	return nil
}

func (t *Tenant) FinishOnboarding(by int) error {
	if err := t.need(by, PermConfig); err != nil {
		return err
	}
	if t.Industry == "" {
		return errors.New("falta configurar la ticketera")
	}
	t.Onboarded = true
	return nil
}

// --- Incidentes ---

type NewIncident struct {
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Service     string            `json:"service"`
	Priority    string            `json:"priority"`
	Fields      map[string]string `json:"fields"`
	AssigneeID  int               `json:"assigneeId"`
	ProblemID   int               `json:"problemId"`
}

func (t *Tenant) CreateIncident(in NewIncident, by int) (*Incident, error) {
	if err := t.need(by, PermCreate); err != nil {
		return nil, err
	}
	// Asignar y vincular son decisiones de quien resuelve; quien solo reporta no las toma.
	if !t.can(by, PermResolve) {
		in.AssigneeID, in.ProblemID = 0, 0
	}
	if in.Title = strings.TrimSpace(in.Title); in.Title == "" {
		return nil, errors.New("contá qué está pasando")
	}
	if !has(t.Config.Services, in.Service) {
		return nil, errors.New("elegí un servicio del catálogo")
	}
	if !has(priorities, in.Priority) {
		return nil, errors.New("prioridad inválida")
	}
	if in.AssigneeID != 0 && t.user(in.AssigneeID) == nil {
		return nil, fmt.Errorf("usuario %d: %w", in.AssigneeID, errNotFound)
	}
	fields := map[string]string{}
	for _, f := range t.Config.Fields {
		v := strings.TrimSpace(in.Fields[f.Key])
		switch {
		case v == "" && f.Required:
			return nil, fmt.Errorf("falta completar: %s", f.Label)
		case v == "":
			continue
		case f.Type == "number":
			if _, err := strconv.ParseFloat(v, 64); err != nil {
				return nil, fmt.Errorf("%s tiene que ser un número", f.Label)
			}
		case f.Type == "select" && !has(f.Options, v):
			return nil, fmt.Errorf("opción inválida en %s", f.Label)
		}
		fields[f.Key] = v
	}
	if in.ProblemID != 0 {
		if _, err := t.problem(in.ProblemID); err != nil {
			return nil, err
		}
	}
	now := time.Now()
	inc := &Incident{ID: len(t.Incidents) + 1, Title: in.Title, Description: in.Description, Service: in.Service,
		Priority: in.Priority, Status: t.Config.States[0], Fields: fields, CreatedBy: by, AssigneeID: in.AssigneeID, CreatedAt: now}
	if t.Config.Modules.SLA {
		due := now.Add(time.Duration(t.Config.SLAHours[in.Priority]) * time.Hour)
		inc.DueAt = &due
	}
	t.Incidents = append(t.Incidents, inc)
	if in.ProblemID != 0 {
		return inc, t.link(inc, in.ProblemID, by)
	}
	return inc, nil
}

func (t *Tenant) link(inc *Incident, problemID, by int) error {
	p, err := t.problem(problemID)
	if err != nil {
		return err
	}
	if p.Status == Resuelto {
		return errors.New("el problema ya está resuelto")
	}
	inc.ProblemID = p.ID
	p.log(by, "", "#%d vinculado al problema", inc.ID)
	return nil
}

func (t *Tenant) setStatus(inc *Incident, status string, by int) error {
	if !has(t.Config.States, status) {
		return errors.New("estado inválido")
	}
	wasOpen := inc.ResolvedAt == nil
	inc.Status = status
	if status != t.closedState() {
		inc.ResolvedAt = nil
		return nil
	}
	if wasOpen {
		now := time.Now()
		inc.ResolvedAt = &now
		if p, err := t.problem(inc.ProblemID); err == nil && p.Workaround != "" && p.Status != Resuelto {
			p.log(by, "", "#%d resuelto con workaround", inc.ID)
		}
	}
	return nil
}

// IncidentPatch: solo se aplican los campos presentes.
type IncidentPatch struct {
	Status     *string `json:"status"`
	AssigneeID *int    `json:"assigneeId"`
	ProblemID  *int    `json:"problemId"`
}

func (t *Tenant) UpdateIncident(id int, in IncidentPatch, by int) error {
	if err := t.need(by, PermResolve); err != nil {
		return err
	}
	inc, err := t.incident(id)
	if err != nil {
		return err
	}
	if in.AssigneeID != nil {
		if *in.AssigneeID != 0 && t.user(*in.AssigneeID) == nil {
			return fmt.Errorf("usuario %d: %w", *in.AssigneeID, errNotFound)
		}
		inc.AssigneeID = *in.AssigneeID
	}
	if in.ProblemID != nil {
		if err := t.link(inc, *in.ProblemID, by); err != nil {
			return err
		}
	}
	if in.Status != nil {
		return t.setStatus(inc, *in.Status, by)
	}
	return nil
}

// --- Problemas ---

func (t *Tenant) CreateProblem(title, desc, service string, incidentIDs []int, by int) (*Problem, error) {
	if err := t.needProblems(by); err != nil {
		return nil, err
	}
	if title = strings.TrimSpace(title); title == "" {
		return nil, errors.New("ponele un título al problema")
	}
	if !has(t.Config.Services, service) {
		return nil, errors.New("elegí un servicio del catálogo")
	}
	for _, id := range incidentIDs {
		if _, err := t.incident(id); err != nil {
			return nil, err
		}
	}
	p := &Problem{ID: len(t.Problems) + 1, Title: title, Description: desc, Service: service,
		Status: Identificado, OwnerID: by, CreatedAt: time.Now()}
	if len(incidentIDs) > 0 {
		p.log(by, "", "Problema creado desde %d incidentes recurrentes", len(incidentIDs))
	} else {
		p.log(by, "", "Problema creado manualmente")
	}
	t.Problems = append(t.Problems, p)
	for _, id := range incidentIDs {
		t.Incidents[id-1].ProblemID = p.ID
	}
	return p, nil
}

type Advance struct {
	RootCause  string `json:"rootCause"`
	Workaround string `json:"workaround"`
	Solution   string `json:"solution"`
}

// Advance mueve el problema al siguiente estado, exigiendo lo que cada etapa necesita.
func (t *Tenant) Advance(id int, in Advance, by int) error {
	if err := t.needProblems(by); err != nil {
		return err
	}
	p, err := t.problem(id)
	if err != nil {
		return err
	}
	switch p.Status {
	case Identificado:
		p.Status = EnAnalisis
		p.log(by, "analisis", "Análisis de causa raíz iniciado")
	case EnAnalisis:
		if strings.TrimSpace(in.RootCause) == "" {
			return errors.New("para registrar un error conocido hay que documentar la causa raíz")
		}
		p.RootCause, p.Workaround = in.RootCause, in.Workaround
		p.Status = ErrorConocido
		p.log(by, "conocido", "Registrado como error conocido")
	case ErrorConocido:
		if strings.TrimSpace(in.Solution) == "" {
			return errors.New("falta la solución definitiva")
		}
		p.Solution, p.Status = in.Solution, Resuelto
		closed := 0
		for _, inc := range t.Incidents {
			if inc.ProblemID == p.ID && inc.ResolvedAt == nil {
				now := time.Now()
				inc.Status, inc.ResolvedAt = t.closedState(), &now
				closed++
			}
		}
		p.log(by, "resuelto", "Resuelto · %d incidentes cerrados", closed)
	default:
		return errors.New("el problema ya está resuelto")
	}
	return nil
}

func (t *Tenant) SetOwner(id, ownerID, by int) error {
	if err := t.needProblems(by); err != nil {
		return err
	}
	p, err := t.problem(id)
	if err != nil {
		return err
	}
	u := t.user(ownerID)
	if u == nil {
		return fmt.Errorf("usuario %d: %w", ownerID, errNotFound)
	}
	p.OwnerID = ownerID
	p.log(by, "", "Responsable: %s", u.Name)
	return nil
}

func (t *Tenant) Comment(id int, text string, by int) error {
	if err := t.needProblems(by); err != nil {
		return err
	}
	p, err := t.problem(id)
	if err != nil {
		return err
	}
	if text = strings.TrimSpace(text); text == "" {
		return errors.New("el comentario está vacío")
	}
	p.log(by, "comentario", "%s", text)
	return nil
}

// Suggestions agrupa por servicio los incidentes sin problema de los últimos RecurrenceDays días
// (abiertos o no: que se hayan resuelto uno por uno es justamente la señal de un problema de fondo).
func (t *Tenant) Suggestions() []Suggestion {
	out := []Suggestion{}
	if !t.Config.Modules.Problems {
		return out
	}
	since := time.Now().AddDate(0, 0, -t.Config.RecurrenceDays)
	by := map[string][]int{}
	for _, inc := range t.Incidents {
		if inc.ProblemID == 0 && inc.CreatedAt.After(since) {
			by[inc.Service] = append(by[inc.Service], inc.ID)
		}
	}
	for svc, ids := range by {
		if len(ids) >= t.Config.RecurrenceMin {
			out = append(out, Suggestion{svc, ids})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].IncidentIDs) != len(out[j].IncidentIDs) {
			return len(out[i].IncidentIDs) > len(out[j].IncidentIDs)
		}
		return out[i].Service < out[j].Service
	})
	return out
}

// --- Store ---

// ponytail: todas las cuentas en un archivo JSON que se reescribe entero en cada cambio.
// Alcanza para una demo; pasar a MySQL/Postgres con tenant_id cuando haya datos reales.
type Store struct {
	mu       sync.Mutex
	path     string // "" = solo en memoria (tests)
	Tenants  []*Tenant
	sessions map[string]session
}

func (s *Store) save() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s.Tenants, "", " ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil { // tiene hashes de contraseñas
		return err
	}
	return os.Rename(tmp, s.path)
}

// open carga el archivo de datos o, si no existe, arranca con las cuentas de demo.
func open(path string) (*Store, error) {
	s := &Store{path: path, sessions: map[string]session{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		s.Tenants = seed()
		return s, s.save()
	}
	if err != nil {
		return nil, err
	}
	return s, json.Unmarshal(data, &s.Tenants)
}

// --- HTTP ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, errNotFound):
		status = http.StatusNotFound
	case errors.Is(err, errForbidden):
		status = http.StatusForbidden
	case errors.Is(err, errUnauthorized):
		status = http.StatusUnauthorized
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func body(r *http.Request, v any) error {
	if json.NewDecoder(r.Body).Decode(v) != nil {
		return errors.New("JSON inválido")
	}
	return nil
}

func num(s string) int {
	n, _ := strconv.Atoi(s) // inválido => 0 => no encontrado
	return n
}

// view es lo que ve el usuario logueado: su cuenta, sin contraseñas y recortada según sus permisos.
type view struct {
	ID          int          `json:"id"`
	Name        string       `json:"name"`
	Industry    string       `json:"industry"`
	Onboarded   bool         `json:"onboarded"`
	OwnerID     int          `json:"ownerId"`
	Me          int          `json:"me"`
	MaxUsers    int          `json:"maxUsers"`
	Config      Config       `json:"config"`
	Roles       []Role       `json:"roles"`
	Users       []User       `json:"users"`
	Incidents   []*Incident  `json:"incidents"`
	Problems    []*Problem   `json:"problems"`
	Suggestions []Suggestion `json:"suggestions"`
}

func (t *Tenant) view(me int) view {
	v := view{ID: t.ID, Name: t.Name, Industry: t.Industry, Onboarded: t.Onboarded, OwnerID: t.OwnerID, Me: me,
		MaxUsers: maxUsers, Config: t.Config, Roles: t.Roles, Incidents: []*Incident{}, Problems: []*Problem{}, Suggestions: []Suggestion{}}
	for _, u := range t.Users {
		u.Hash = ""
		v.Users = append(v.Users, u)
	}
	// Quien solo reporta ve sus propios incidentes; el resto requiere resolver tickets o ver el panel.
	all := t.can(me, PermResolve) || t.can(me, PermPanel)
	for _, inc := range t.Incidents {
		if all || inc.CreatedBy == me {
			v.Incidents = append(v.Incidents, inc)
		}
	}
	if t.Config.Modules.Problems && (t.can(me, PermProblems) || t.can(me, PermResolve) || t.can(me, PermPanel)) {
		v.Problems = t.Problems
	}
	if t.can(me, PermProblems) {
		v.Suggestions = t.Suggestions()
	}
	return v
}

// handle autentica, ejecuta fn sobre la cuenta del usuario, persiste y responde
// siempre con el estado completo de la cuenta: el front no tiene que re-consultar.
func (s *Store) handle(fn func(t *Tenant, r *http.Request, by int) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t, by, err := s.auth(r)
		if err == nil {
			err = fn(t, r, by)
		}
		if err == nil && r.Method != http.MethodGet {
			err = s.save()
		}
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, t.view(by))
	}
}

func (s *Store) Handler() http.Handler {
	mux := http.NewServeMux()

	// Sin sesión: contratar (crear la cuenta), entrar y las plantillas del onboarding.
	token := func(w http.ResponseWriter, tok string, err error) {
		if err == nil {
			err = s.save()
		}
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"token": tok})
	}
	mux.HandleFunc("POST /api/signup", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Company, Name, Email, Password string }
		if err := body(r, &in); err != nil {
			fail(w, err)
			return
		}
		tok, err := s.Signup(in.Company, in.Name, in.Email, in.Password)
		token(w, tok, err)
	})
	mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Email, Password string }
		if err := body(r, &in); err != nil {
			fail(w, err)
			return
		}
		tok, err := s.Login(in.Email, in.Password)
		token(w, tok, err)
	})
	mux.HandleFunc("POST /api/logout", func(w http.ResponseWriter, r *http.Request) {
		delete(s.sessions, bearer(r))
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/templates", func(w http.ResponseWriter, r *http.Request) {
		type row struct {
			Key      string `json:"key"`
			Industry string `json:"industry"`
			Config   Config `json:"config"`
		}
		out := []row{}
		for _, k := range templateOrder {
			c, _ := templates[k].build(full)
			out = append(out, row{k, templates[k].Industry, c})
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.HandleFunc("GET /api/state", s.handle(func(*Tenant, *http.Request, int) error { return nil }))
	mux.HandleFunc("POST /api/onboarding", s.handle(func(t *Tenant, r *http.Request, by int) error {
		var in Setup
		if err := body(r, &in); err != nil {
			return err
		}
		return t.Onboard(in, by)
	}))
	mux.HandleFunc("POST /api/onboarding/finish", s.handle(func(t *Tenant, r *http.Request, by int) error {
		return t.FinishOnboarding(by)
	}))
	mux.HandleFunc("PUT /api/config", s.handle(func(t *Tenant, r *http.Request, by int) error {
		var in Config
		if err := body(r, &in); err != nil {
			return err
		}
		return t.SetConfig(in, by)
	}))

	// Usuarios y roles de la cuenta.
	mux.HandleFunc("POST /api/users", s.handle(func(t *Tenant, r *http.Request, by int) error {
		var in struct {
			Name, Email, Password string
			RoleID                int `json:"roleId"`
		}
		if err := body(r, &in); err != nil {
			return err
		}
		return s.AddUser(t, in.Name, in.Email, in.Password, in.RoleID, by)
	}))
	mux.HandleFunc("PUT /api/users/{id}", s.handle(func(t *Tenant, r *http.Request, by int) error {
		var in struct {
			RoleID int `json:"roleId"`
		}
		if err := body(r, &in); err != nil {
			return err
		}
		return t.SetUserRole(num(r.PathValue("id")), in.RoleID, by)
	}))
	mux.HandleFunc("DELETE /api/users/{id}", s.handle(func(t *Tenant, r *http.Request, by int) error {
		return s.DeleteUser(t, num(r.PathValue("id")), by)
	}))
	saveRole := func(t *Tenant, r *http.Request, by int) error {
		var in struct {
			Name  string
			Perms []string
		}
		if err := body(r, &in); err != nil {
			return err
		}
		return t.SaveRole(num(r.PathValue("id")), in.Name, in.Perms, by)
	}
	mux.HandleFunc("POST /api/roles", s.handle(saveRole))
	mux.HandleFunc("PUT /api/roles/{id}", s.handle(saveRole))
	mux.HandleFunc("DELETE /api/roles/{id}", s.handle(func(t *Tenant, r *http.Request, by int) error {
		return t.DeleteRole(num(r.PathValue("id")), by)
	}))

	mux.HandleFunc("POST /api/incidents", s.handle(func(t *Tenant, r *http.Request, by int) error {
		var in NewIncident
		if err := body(r, &in); err != nil {
			return err
		}
		_, err := t.CreateIncident(in, by)
		return err
	}))
	mux.HandleFunc("POST /api/incidents/{id}", s.handle(func(t *Tenant, r *http.Request, by int) error {
		var in IncidentPatch
		if err := body(r, &in); err != nil {
			return err
		}
		return t.UpdateIncident(num(r.PathValue("id")), in, by)
	}))
	mux.HandleFunc("POST /api/problems", s.handle(func(t *Tenant, r *http.Request, by int) error {
		var in struct {
			Title, Description, Service string
			IncidentIDs                 []int `json:"incidentIds"`
		}
		if err := body(r, &in); err != nil {
			return err
		}
		_, err := t.CreateProblem(in.Title, in.Description, in.Service, in.IncidentIDs, by)
		return err
	}))
	mux.HandleFunc("POST /api/problems/{id}/advance", s.handle(func(t *Tenant, r *http.Request, by int) error {
		var in Advance
		if err := body(r, &in); err != nil {
			return err
		}
		return t.Advance(num(r.PathValue("id")), in, by)
	}))
	mux.HandleFunc("POST /api/problems/{id}/owner", s.handle(func(t *Tenant, r *http.Request, by int) error {
		var in struct {
			OwnerID int `json:"ownerId"`
		}
		if err := body(r, &in); err != nil {
			return err
		}
		return t.SetOwner(num(r.PathValue("id")), in.OwnerID, by)
	}))
	mux.HandleFunc("POST /api/problems/{id}/comments", s.handle(func(t *Tenant, r *http.Request, by int) error {
		var in struct{ Text string }
		if err := body(r, &in); err != nil {
			return err
		}
		return t.Comment(num(r.PathValue("id")), in.Text, by)
	}))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ponytail: CORS abierto, es una demo local (la sesión va en un header, no en cookies). Restringir el origen antes de exponerlo.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		// ponytail: un lock global por request; alcanza de sobra para una demo.
		s.mu.Lock()
		defer s.mu.Unlock()
		mux.ServeHTTP(w, r)
	})
}

func main() {
	path := flag.String("data", "data.json", "archivo de datos")
	reset := flag.Bool("reset", false, "descarta los datos guardados y vuelve a las cuentas de demo")
	flag.Parse()
	if *reset {
		os.Remove(*path)
	}
	s, err := open(*path)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("TaaS backend en http://localhost:8080 · %d cuentas · datos en %s", len(s.Tenants), *path)
	log.Fatal(http.ListenAndServe(":8080", s.Handler()))
}
