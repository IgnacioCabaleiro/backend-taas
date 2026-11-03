package repository_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"taas-backend/internal/domain"
	"taas-backend/internal/repository"
)

// Un data.json de antes del ciclo de vida del incidente: sin historial, primera respuesta, pausas ni cierre.
const legacy = `[{"id": 1, "name": "Vieja", "industry": "Soporte IT", "onboarded": true, "ownerId": 1,
 "config": {"modules": {"sla": true, "problems": true}, "services": ["VPN"], "states": ["Nuevo", "En curso", "Resuelto"],
  "slaHours": {"alta": 1, "media": 4, "baja": 12}, "fields": [], "recurrenceMin": 2, "recurrenceDays": 7},
 "roles": [{"id": 1, "name": "Administrador", "perms": ["panel", "crear", "resolver", "problemas", "config"], "locked": true}],
 "users": [{"id": 1, "name": "Titular", "email": "t@vieja.test", "roleId": 1}], "nextUserId": 2, "nextRoleId": 2,
 "incidents": [{"id": 1, "title": "x", "description": "", "service": "VPN", "priority": "alta", "status": "Resuelto",
  "fields": {}, "createdBy": 1, "assigneeId": 0, "problemId": 0, "createdAt": "2026-01-01T10:00:00Z",
  "dueAt": "2026-01-01T11:00:00Z", "resolvedAt": "2026-01-01T12:00:00Z"}],
 "problems": []}]`

func TestLoadLegacyData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	tenants, err := repository.NewJSONTenants(path)
	if err != nil {
		t.Fatal(err)
	}
	tn, _ := tenants.Get(1)
	c := tn.Config
	// Sin primera respuesta: copia la resolución de la propia cuenta (1/4/12), no los valores de una plantilla.
	if c.AutoCloseDays != 2 || fmt.Sprint(c.ResponseHours) != "map[alta:1 baja:12 media:4]" ||
		c.PauseStates == nil || len(c.PauseStates) != 0 {
		t.Fatalf("faltan los valores por defecto: %+v", c)
	}
	if reflect.ValueOf(c.ResponseHours).UnsafePointer() == reflect.ValueOf(c.SLAHours).UnsafePointer() {
		t.Fatal("la primera respuesta tiene que ser una copia de la resolución, no el mismo mapa")
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("la configuración cargada tiene que ser válida: %v", err)
	}

	// El resuelto viejo (sin historial: la cuenta arranca en la resolución) se cierra al cargar, a los 2 días,
	// y el cierre queda guardado.
	inc := tn.Incidents[0]
	closedAt := time.Date(2026, 1, 3, 12, 0, 0, 0, time.UTC)
	if inc.ResolutionCode != "" || inc.ReopenCount != 0 || inc.ClosedAt == nil || !inc.ClosedAt.Equal(closedAt) ||
		len(inc.History) != 1 || inc.History[0].Kind != domain.EvClosed || inc.History[0].UserID != 0 {
		t.Fatalf("incidente viejo mal normalizado: %+v", inc)
	}
	var saved []domain.Tenant
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &saved); err != nil || saved[0].Incidents[0].ClosedAt == nil || saved[0].Config.AutoCloseDays != 2 {
		t.Fatalf("el cierre automático de la carga no se guardó: %v", err)
	}
}

// Un autoCloseDays en 0 (inválido) también se carga con el valor por defecto.
func TestLoadZeroAutoCloseDays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	data := strings.Replace(legacy, `"recurrenceDays": 7}`, `"recurrenceDays": 7, "autoCloseDays": 0}`, 1)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	tenants, err := repository.NewJSONTenants(path)
	if err != nil {
		t.Fatal(err)
	}
	if tn, _ := tenants.Get(1); tn.Config.AutoCloseDays != 2 {
		t.Fatalf("autoCloseDays=%d", tn.Config.AutoCloseDays)
	}
}

// Una cuenta guardada con primeras respuestas mayores que su resolución (antes se podía) se ajusta al cargar:
// cada prioridad baja a su resolución y la configuración vuelve a ser válida.
func TestLoadClampsResponseHours(t *testing.T) {
	sla := `"slaHours": {"alta": 1, "media": 4, "baja": 12}`
	data := strings.Replace(legacy, sla, sla+`, "responseHours": {"alta": 4, "media": 8, "baja": 24}`, 1)
	if data == legacy {
		t.Fatal("el fixture cambió: no se pudo agregar responseHours")
	}
	path := filepath.Join(t.TempDir(), "data.json")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	tenants, err := repository.NewJSONTenants(path)
	if err != nil {
		t.Fatal(err)
	}
	tn, _ := tenants.Get(1)
	c := tn.Config
	if fmt.Sprint(c.ResponseHours) != "map[alta:1 baja:12 media:4]" {
		t.Fatalf("la primera respuesta tiene que bajar a la resolución: %v", c.ResponseHours)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("la configuración cargada tiene que ser válida: %v", err)
	}
}
