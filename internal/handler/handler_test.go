package handler

import (
	"encoding/json"
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
