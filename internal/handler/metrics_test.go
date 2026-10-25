package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"taas-backend/internal/domain"
)

// Quién puede pedir las métricas de SLA y con qué períodos.
func TestSLAMetricsEndpoint(t *testing.T) {
	h, svc, tok := newServer(t)
	in := domain.NewIncident{Title: "x", Service: "VPN", Priority: "alta", Fields: map[string]string{"sede": "Remoto"}}
	svc.Incidents.Create(1, 3, in)

	for _, c := range []struct {
		name, token, query string
		want               int
	}{
		{"el titular, por defecto 30 días", tok[1], "", 200},
		{"el agente tiene el panel", tok[2], "?days=7", 200},
		{"90 días", tok[1], "?days=90", 200},
		{"el solicitante no ve el panel", tok[3], "", 403},
		{"sin sesión", "", "", 401},
		{"período inválido", tok[1], "?days=5", 400},
		{"período que no es número", tok[1], "?days=abc", 400},
	} {
		if rec := getMetrics(h, "/api/metrics/sla"+c.query, c.token); rec.Code != c.want {
			t.Errorf("%s: esperaba %d, obtuve %d (%s)", c.name, c.want, rec.Code, rec.Body.String())
		}
	}

	rec := getMetrics(h, "/api/metrics/sla?days=7", tok[1])
	var m domain.SLAMetrics
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.Days != 7 || m.Total.Incidents != 1 || len(m.Trend) != 7 {
		t.Errorf("métricas inesperadas: %+v", m)
	}
}

// Sin el módulo SLA no hay métricas de SLA.
func TestSLAMetricsNeedsModule(t *testing.T) {
	h, svc, tok := newServer(t)
	state, err := svc.State.View(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	c := state.Config
	c.Modules.SLA = false
	if err := svc.Setup.SetConfig(1, 1, c); err != nil {
		t.Fatal(err)
	}
	if rec := getMetrics(h, "/api/metrics/sla", tok[1]); rec.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400 con el módulo apagado, obtuve %d", rec.Code)
	}
}

// getMetrics hace un GET con la sesión del token (sin sesión si está vacío).
func getMetrics(h http.Handler, path, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}
