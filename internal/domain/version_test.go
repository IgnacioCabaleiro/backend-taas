package domain

import (
	"strings"
	"testing"
	"time"
)

func baseConfig() Config {
	return Config{Services: []string{"VPN", "Correo"}, States: []string{"Nuevo", "En curso", "Resuelto"},
		SLAHours: map[string]int{"alta": 4, "media": 8, "baja": 24}, ResponseHours: map[string]int{"alta": 1, "media": 2, "baja": 4},
		Fields: []Field{{Key: "sede", Label: "Sede", Type: "select", Options: []string{"A", "B"}}}, PauseStates: []string{},
		RecurrenceMin: 2, RecurrenceDays: 7, AutoCloseDays: 2}
}

// Todo lo que se puede configurar tiene que aparecer en el resumen: si no, el cambio no generaría una versión.
func TestConfigChanges(t *testing.T) {
	cases := []struct {
		name   string
		change func(c *Config)
		want   string
	}{
		{"módulo SLA", func(c *Config) { c.Modules.SLA = true }, "SLA activado"},
		{"módulo problemas", func(c *Config) { c.Modules.Problems = true }, "Gestión de problemas activado"},
		{"servicio nuevo", func(c *Config) { c.Services = append(c.Services, "WiFi") }, "Servicio agregado: WiFi"},
		{"servicio quitado", func(c *Config) { c.Services = c.Services[:1] }, "Servicio eliminado: Correo"},
		{"servicio renombrado", func(c *Config) { c.Services[1] = "Mail" }, "Servicio: Correo → Mail"},
		{"estados reordenados", func(c *Config) { c.States[0], c.States[1] = c.States[1], c.States[0] }, "Estado: cambió el orden"},
		{"SLA", func(c *Config) { c.SLAHours["alta"] = 9 }, "SLA de prioridad alta: 4 h → 9 h"},
		{"primera respuesta", func(c *Config) { c.ResponseHours["baja"] = 9 }, "Primera respuesta de prioridad baja: 4 h → 9 h"},
		{"campo nuevo", func(c *Config) { c.Fields = append(c.Fields, Field{Label: "Piso"}) }, "Campo agregado: Piso"},
		{"opciones de un campo", func(c *Config) { c.Fields[0].Options = []string{"A", "C"} }, "Campos del ticket modificados"},
		{"campo obligatorio", func(c *Config) { c.Fields[0].Required = true }, "Campos del ticket modificados"},
		{"pausa", func(c *Config) { c.PauseStates = []string{"En curso"} }, "Estados que pausan el SLA: En curso"},
		{"recurrencia", func(c *Config) { c.RecurrenceMin = 3 }, "Recurrencia: 3 incidentes en 7 días"},
		{"cierre automático", func(c *Config) { c.AutoCloseDays = 5 }, "Cierre automático: 2 → 5 días"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := baseConfig()
			tc.change(&c)
			got := ConfigChanges(baseConfig(), c)
			if !strings.Contains(strings.Join(got, "|"), tc.want) {
				t.Fatalf("esperaba %q, obtuve %v", tc.want, got)
			}
		})
	}
	if got := ConfigChanges(baseConfig(), baseConfig()); len(got) != 0 {
		t.Fatalf("dos configuraciones iguales no tienen cambios: %v", got)
	}
}

func TestVersions(t *testing.T) {
	tn := &Tenant{Config: baseConfig()}
	now := time.Now()
	tn.EnsureFirstVersion(now, 1)
	tn.EnsureFirstVersion(now, 2)
	if len(tn.ConfigVersions) != 1 || tn.ConfigVersions[0].UserID != 1 {
		t.Fatalf("la primera versión se crea una sola vez: %+v", tn.ConfigVersions)
	}
	if v, err := tn.Version(1); err != nil || v.ID != 1 {
		t.Fatalf("no encontró la versión 1: %v", err)
	}
	if _, err := tn.Version(2); err == nil {
		t.Fatal("la versión 2 no existe")
	}
	tn.Config.SLAHours["alta"] = 99 // la foto es independiente de la configuración vigente
	if tn.ConfigVersions[0].Config.SLAHours["alta"] != 4 {
		t.Fatal("la versión comparte datos con la configuración vigente")
	}
}
