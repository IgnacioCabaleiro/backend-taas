package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"taas-backend/internal/domain"
	"taas-backend/internal/repository"
	"taas-backend/internal/service"
)

// Un cliente que manda el body de a poco no puede trabar al resto de los pedidos.
func TestSlowBodyDoesNotBlock(t *testing.T) {
	tenants, _ := repository.NewJSONTenants("")
	h := New(service.New(tenants, repository.NewMemorySessions()))

	body, w := io.Pipe()
	defer w.Close()
	go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/incidents", body))
	w.Write([]byte("{")) // y nunca termina

	done := make(chan int)
	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/state", nil))
		done <- rec.Code
	}()
	select {
	case code := <-done:
		if code != http.StatusUnauthorized {
			t.Fatalf("esperaba 401 sin sesión, obtuve %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("el pedido quedó bloqueado por otro con el body incompleto")
	}
}

// Un body más grande que el límite se rechaza con 413.
func TestBodyTooLarge(t *testing.T) {
	tenants, _ := repository.NewJSONTenants("")
	h := New(service.New(tenants, repository.NewMemorySessions()))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/login", strings.NewReader(strings.Repeat("x", 2<<20))))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("esperaba 413, obtuve %d", rec.Code)
	}
}

func TestBodyIncomplete(t *testing.T) {
	tenants, _ := repository.NewJSONTenants("")
	h := New(service.New(tenants, repository.NewMemorySessions()))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/login", iotest.ErrReader(io.ErrUnexpectedEOF)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400, obtuve %d", rec.Code)
	}
}

// newServer arma una cuenta IT con titular (1), agente (2) y solicitante (3), con una sesión abierta para cada uno.
func newServer(t *testing.T) (http.Handler, *service.Services, map[int]string) {
	t.Helper()
	tenants, _ := repository.NewJSONTenants("")
	sessions := repository.NewMemorySessions()
	svc := service.New(tenants, sessions)
	industry, cfg, err := service.BuildConfig("it", service.Full)
	if err != nil {
		t.Fatal(err)
	}
	tn := &domain.Tenant{Name: "Demo", Industry: industry, Onboarded: true, OwnerID: 1, Config: cfg, Roles: service.DefaultRoles(),
		NextUserID: 4, NextRoleID: 4, Incidents: []*domain.Incident{}, Problems: []*domain.Problem{}}
	tokens := map[int]string{}
	for id, name := range []string{"Titular", "Agente", "Solicitante"} {
		tn.Users = append(tn.Users, domain.User{ID: id + 1, Name: name, Email: strings.ToLower(name) + "@demo.test", RoleID: id + 1})
	}
	if err := tenants.Create(tn); err != nil {
		t.Fatal(err)
	}
	for _, u := range tn.Users {
		tokens[u.ID] = sessions.Create(repository.Session{TenantID: tn.ID, UserID: u.ID})
	}
	return New(svc), svc, tokens
}

// call hace el pedido y devuelve el código y, si salió bien, el estado de la cuenta.
func call(t *testing.T, h http.Handler, token, path, body string) (int, service.View) {
	t.Helper()
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	var v service.View
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
	}
	return rec.Code, v
}

func TestIncidentComments(t *testing.T) {
	h, svc, tok := newServer(t)
	in := domain.NewIncident{Title: "x", Service: "VPN", Priority: "alta", Fields: map[string]string{"sede": "Remoto"}}
	svc.Incidents.Create(1, 3, in) // #1 del solicitante
	svc.Incidents.Create(1, 2, in) // #2 del agente

	for _, c := range []struct {
		user int
		path string
		body string
		want int
	}{
		{2, "/api/incidents/1/comments", `{"text": "lo miro", "public": true}`, 200},
		{2, "/api/incidents/1/comments", `{"text": "interna", "public": false}`, 200},
		{3, "/api/incidents/1/comments", `{"text": "gracias", "public": true}`, 200},
		{3, "/api/incidents/1/comments", `{"text": "interna", "public": false}`, 403},
		{3, "/api/incidents/2/comments", `{"text": "ajeno", "public": true}`, 403},
		{2, "/api/incidents/1/comments", `{"text": "  ", "public": true}`, 400},
		{2, "/api/incidents/9/comments", `{"text": "x", "public": true}`, 404},
	} {
		if got, _ := call(t, h, tok[c.user], c.path, c.body); got != c.want {
			t.Errorf("usuario %d %s %s: esperaba %d, obtuve %d", c.user, c.path, c.body, c.want, got)
		}
	}
	_, v := call(t, h, tok[3], "/api/incidents/1/comments", `{"text": "otra", "public": true}`)
	for _, e := range v.Incidents[0].History {
		if e.Kind == domain.EvNote {
			t.Fatal("el solicitante recibe una nota interna")
		}
	}
}

func TestIncidentReopenAndClose(t *testing.T) {
	h, svc, tok := newServer(t)
	in := domain.NewIncident{Title: "x", Service: "VPN", Priority: "alta", Fields: map[string]string{"sede": "Remoto"}}
	svc.Incidents.Create(1, 3, in) // #1 y #2 del solicitante
	svc.Incidents.Create(1, 3, in)

	for _, c := range []struct {
		user int
		path string
		body string
		want int
	}{
		{2, "/api/incidents/1/reopen", `{"reason": "sigue"}`, 400}, // está abierto
		{2, "/api/incidents/1/close", `{}`, 400},                   // está abierto
		{2, "/api/incidents/1", `{"status": "Resuelto"}`, 400},     // sin código ni nota
		{2, "/api/incidents/1", `{"status": "Resuelto", "resolutionCode": "solucionado", "resolutionNote": "ok"}`, 200},
		{2, "/api/incidents/1", `{"status": "En curso"}`, 400}, // resuelto
		{3, "/api/incidents/1/reopen", `{"reason": "volvió a pasar"}`, 403},
		{3, "/api/incidents/1/close", `{}`, 403},
		{2, "/api/incidents/1/reopen", `{"reason": " "}`, 400},
		{2, "/api/incidents/1/reopen", `{"reason": "volvió a pasar"}`, 200},
		{2, "/api/incidents/1", `{"status": "Resuelto", "resolutionCode": "solucionado", "resolutionNote": "ok"}`, 200},
		{2, "/api/incidents/1/close", `{}`, 200},
		{2, "/api/incidents/1/close", `{}`, 400},               // ya está cerrado
		{2, "/api/incidents/1/reopen", `{"reason": "x"}`, 400}, // cerrado
		{2, "/api/incidents/1", `{"assigneeId": 2}`, 400},      // cerrado
		{3, "/api/incidents/1/comments", `{"text": "x", "public": true}`, 400},
		{2, "/api/incidents/2", `{"status": "Resuelto", "resolutionCode": "duplicado", "resolutionNote": "es el #1"}`, 200},
		{2, "/api/incidents/2/close", `{}`, 400}, // duplicado: cerró al resolver
		{3, "/api/incidents", `{"title": "otra vez", "service": "VPN", "priority": "alta", "fields": {"sede": "Remoto"}, "relatedIncidentId": 1}`, 200},
		{3, "/api/incidents", `{"title": "otra vez", "service": "VPN", "priority": "alta", "fields": {"sede": "Remoto"}, "relatedIncidentId": 3}`, 400},
		{2, "/api/incidents/9/close", `{}`, 404},
	} {
		got, v := call(t, h, tok[c.user], c.path, c.body)
		if got != c.want {
			t.Errorf("usuario %d %s %s: esperaba %d, obtuve %d", c.user, c.path, c.body, c.want, got)
		}
		if got == 200 && strings.HasSuffix(c.path, "/close") && v.Incidents[0].ClosedAt == nil {
			t.Error("la respuesta tiene que traer el incidente cerrado")
		}
	}
}

// Solo se asigna a quien atiende incidentes: asignar a quien solo reporta es un 400.
func TestAssignIncident(t *testing.T) {
	h, svc, tok := newServer(t)
	in := domain.NewIncident{Title: "x", Service: "VPN", Priority: "alta", Fields: map[string]string{"sede": "Remoto"}}
	svc.Incidents.Create(1, 3, in) // #1 del solicitante

	for _, c := range []struct {
		path string
		body string
		want int
	}{
		{"/api/incidents", `{"title": "x", "service": "VPN", "priority": "alta", "fields": {"sede": "Remoto"}, "assigneeId": 3}`, 400},
		{"/api/incidents", `{"title": "x", "service": "VPN", "priority": "alta", "fields": {"sede": "Remoto"}, "assigneeId": 2}`, 200},
		{"/api/incidents/1", `{"assigneeId": 3}`, 400},
		{"/api/incidents/1", `{"assigneeId": 9}`, 404},
		{"/api/incidents/1", `{"assigneeId": 2}`, 200},
		{"/api/incidents/1", `{"assigneeId": 0}`, 200},
	} {
		if got, _ := call(t, h, tok[2], c.path, c.body); got != c.want {
			t.Errorf("%s %s: esperaba %d, obtuve %d", c.path, c.body, c.want, got)
		}
	}
}

// Sacarle resolver o problemas a quien todavía tiene incidentes abiertos asignados o problemas activos, y darle
// un problema a quien no gestiona problemas, son errores de validación (400). Reabrir un incidente cuyo asignado
// ya no tiene resolver lo deja sin asignar.
func TestRoleAndOwnerChecks(t *testing.T) {
	h, svc, tok := newServer(t)
	in := domain.NewIncident{Title: "x", Service: "VPN", Priority: "alta", Fields: map[string]string{"sede": "Remoto"}, AssigneeID: 2}
	svc.Incidents.Create(1, 1, in) // #1 y #2 asignados al agente; el #2, resuelto
	svc.Incidents.Create(1, 1, in)
	resuelto := "Resuelto"
	svc.Incidents.Update(1, 1, 2, domain.IncidentPatch{Status: &resuelto, ResolutionCode: "solucionado", ResolutionNote: "ok"})
	svc.Problems.Create(1, 2, domain.NewProblem{Title: "p", Service: "VPN"}) // P-1, a cargo del agente

	for _, c := range []struct {
		method, path, body string
		want               int
	}{
		{"PUT", "/api/users/2", `{"roleId": 3}`, 400}, // pierde resolver y problemas
		{"PUT", "/api/roles/2", `{"name": "Agente", "perms": ["panel"]}`, 400},
		{"PUT", "/api/roles/2", `{"name": "Agente", "perms": ["panel", "crear", "resolver"]}`, 400}, // pierde problemas
		{"POST", "/api/problems/1/owner", `{"ownerId": 3}`, 400},
		{"POST", "/api/problems/1/owner", `{"ownerId": 9}`, 404},
		{"POST", "/api/problems/1/owner", `{"ownerId": 1}`, 200},
		{"PUT", "/api/roles/2", `{"name": "Agente", "perms": ["panel", "crear", "resolver"]}`, 200},
		{"PUT", "/api/users/2", `{"roleId": 3}`, 400}, // todavía tiene el #1
		{"POST", "/api/incidents/1", `{"assigneeId": 0}`, 200},
		{"PUT", "/api/users/2", `{"roleId": 3}`, 200}, // el #2 está resuelto: no cuenta
		{"POST", "/api/incidents/2/reopen", `{"reason": "sigue"}`, 200},
	} {
		r := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		r.Header.Set("Authorization", "Bearer "+tok[1])
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != c.want {
			t.Errorf("%s %s %s: esperaba %d, obtuve %d (%s)", c.method, c.path, c.body, c.want, rec.Code, rec.Body)
		}
	}
	if v, _ := svc.State.View(1, 1); v.Incidents[1].AssigneeID != 0 {
		t.Fatalf("al reabrirlo, el #2 vuelve sin asignar: %d", v.Incidents[1].AssigneeID)
	}
}

// En el JSON del onboarding, un campo opcional que no vino toma lo de la plantilla; uno que vino en null o en 0
// es un error de validación (400), con el mensaje de Config.Validate.
func TestOnboardingOptionalFields(t *testing.T) {
	tenants, _ := repository.NewJSONTenants("")
	h := New(service.New(tenants, repository.NewMemorySessions()))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/signup",
		strings.NewReader(`{"company": "Chica", "name": "Titular", "email": "chica@demo.test", "password": "clave-de-prueba"}`)))
	var signup struct{ Token string }
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &signup) != nil {
		t.Fatalf("signup: %d %s", rec.Code, rec.Body)
	}

	for _, c := range []struct {
		body string
		want int
		cfg  string // estados, pausa, primera respuesta y cierre automático que quedan
	}{
		{`{"template": "it", "team": true, "sla": true}`, 200, "[Nuevo En curso Resuelto] [] map[alta:4 baja:24 media:8] 2"},
		{`{"template": "it", "sla": true, "waitsRequester": true, "autoCloseDays": 3, "responseHours": {"alta": 1, "media": 2, "baja": 3}}`, 200,
			"[Nuevo Esperando al solicitante Resuelto] [Esperando al solicitante] map[alta:1 baja:3 media:2] 3"},
		{`{"template": "it", "responseHours": {"alta": 1, "media": 2, "baja": 3}, "waitsRequester": true}`, 200,
			"[Nuevo Resuelto] [] map[alta:4 baja:24 media:8] 2"}, // sin SLA, las dos se ignoran
		{`{"template": "it", "sla": true, "responseHours": {"alta": 4, "media": 8, "baja": 24}}`, 200, // igual a la resolución
			"[Nuevo Resuelto] [] map[alta:4 baja:24 media:8] 2"},
		{`{"template": "it", "sla": true, "responseHours": {"alta": 5, "media": 8, "baja": 24}}`, 400, ""}, // supera a la resolución
		{`{"template": "it", "sla": true, "responseHours": {"alta": 2, "media": 8, "baja": 24, "urgente": -5}}`, 200, // no es una prioridad
			"[Nuevo Resuelto] [] map[alta:2 baja:24 media:8] 2"},
		{`{"template": "it", "sla": true, "responseHours": null}`, 400, ""},
		{`{"template": "it", "sla": true, "responseHours": {"alta": null, "media": 2, "baja": 3}}`, 400, ""},
		{`{"template": "it", "sla": true, "responseHours": {"alta": 1, "media": 0, "baja": 3}}`, 400, ""},
		{`{"template": "it", "autoCloseDays": null}`, 400, ""},
		{`{"template": "it", "autoCloseDays": 0}`, 400, ""},
		{`{"template": "it", "autoCloseDays": "3"}`, 400, ""},
	} {
		got, v := call(t, h, signup.Token, "/api/onboarding", c.body)
		cfg := v.Config
		if got != c.want || (got == 200 && fmt.Sprint(cfg.States, cfg.PauseStates, cfg.ResponseHours, cfg.AutoCloseDays) != c.cfg) {
			t.Errorf("%s: esperaba %d %s, obtuve %d %+v", c.body, c.want, c.cfg, got, cfg)
		}
		if got == 200 {
			if raw := getState(t, h, signup.Token); strings.Contains(raw, "urgente") {
				t.Errorf("%s: /api/state trae una clave que no es una prioridad: %s", c.body, raw)
			}
		}
	}
}

// PUT /api/config: una primera respuesta que supera a la resolución de su prioridad es un 400; igual pasa.
func TestConfigResponseNotAfterResolution(t *testing.T) {
	h, _, tok := newServer(t)
	for _, c := range []struct {
		response, resolution int
		want                 int
		msg                  string
	}{
		{5, 4, 400, "la primera respuesta de prioridad alta (5 h) no puede superar a la resolución (4 h)"},
		{4, 4, 200, ""},
		{2, 3, 200, ""},
	} {
		_, cfg, _ := service.BuildConfig("it", service.Full)
		cfg.ResponseHours["alta"], cfg.SLAHours["alta"] = c.response, c.resolution
		cfg.ResponseHours["urgente"], cfg.SLAHours["urgente"] = -5, -5 // no es una prioridad: se descarta
		body, _ := json.Marshal(cfg)
		r := httptest.NewRequest("PUT", "/api/config", strings.NewReader(string(body)))
		r.Header.Set("Authorization", "Bearer "+tok[1])
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		var res struct{ Error string }
		json.Unmarshal(rec.Body.Bytes(), &res)
		if rec.Code != c.want || res.Error != c.msg {
			t.Errorf("respuesta %d h, resolución %d h: esperaba %d %q, obtuve %d %q", c.response, c.resolution, c.want, c.msg, rec.Code, res.Error)
		}
		if raw := getState(t, h, tok[1]); strings.Contains(rec.Body.String(), "urgente") || strings.Contains(raw, "urgente") {
			t.Errorf("respuesta %d h, resolución %d h: quedó guardada una clave que no es una prioridad: %s", c.response, c.resolution, raw)
		}
	}
}

// getState devuelve el cuerpo de GET /api/state.
func getState(t *testing.T, h http.Handler, token string) string {
	t.Helper()
	r := httptest.NewRequest("GET", "/api/state", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/state: %d %s", rec.Code, rec.Body)
	}
	return rec.Body.String()
}
