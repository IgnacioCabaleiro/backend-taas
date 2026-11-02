package domain

import (
	"math"
	"testing"
	"time"
)

var now = time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)

// at: un momento de marzo de 2026 (día, hora) para armar los casos.
func at(day, hour int) *time.Time {
	t := time.Date(2026, 3, day, hour, 0, 0, 0, time.UTC)
	return &t
}

func near(got *float64, want float64) bool { return got != nil && math.Abs(*got-want) < 0.05 }

// slaTenant arma una cuenta con incidentes cuyo resultado se calculó a mano (ahora = 31/03 12:00):
//
//	#1 alta   resuelto en plazo; respondido en plazo                           (agente 2)
//	#2 alta   resuelto fuera de plazo; respondido fuera de plazo               (agente 2)
//	#3 alta   abierto, en plazo (vence 15:00); sin responder y ya venció       (sin asignar)
//	#4 baja   abierto, todo en plazo y lejos del vencimiento                   (sin asignar)
//	#5 alta   resuelto, creado sin módulo SLA (sin vencimientos)               (agente 3)
//	#6 media  abierto en pausa: el reloj está detenido a las 12 de ayer        (sin asignar)
//	#7 alta   de febrero: queda fuera del período
func slaTenant() *Tenant {
	inc := func(id int, prio string, created *time.Time) *Incident {
		return &Incident{ID: id, Title: "inc", Service: "VPN", Priority: prio, CreatedAt: *created, History: []Event{}}
	}
	i1 := inc(1, "alta", at(30, 8))
	i1.DueAt, i1.ResolvedAt, i1.ResponseDueAt, i1.FirstResponseAt, i1.AssigneeID = at(30, 16), at(30, 14), at(30, 12), at(30, 9), 2
	i2 := inc(2, "alta", at(29, 8))
	i2.DueAt, i2.ResolvedAt, i2.ResponseDueAt, i2.FirstResponseAt, i2.AssigneeID = at(29, 16), at(29, 20), at(29, 12), at(29, 13), 2
	i3 := inc(3, "alta", at(31, 7))
	i3.Service = "Correo"
	i3.DueAt, i3.ResponseDueAt = at(31, 15), at(31, 11)
	i4 := inc(4, "baja", at(31, 10))
	i4.DueAt, i4.ResponseDueAt = at(32, 10), at(31, 18) // vence el 1/4
	i5 := inc(5, "alta", at(30, 9))
	i5.ResolvedAt, i5.AssigneeID = at(30, 10), 3
	i6 := inc(6, "media", at(30, 10))
	i6.DueAt, i6.PausedAt = at(30, 14), at(30, 12)
	feb := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	i7 := inc(7, "alta", &feb)
	i7.DueAt, i7.ResolvedAt = at(1, 8), at(1, 20)
	return &Tenant{
		Users:     []User{{ID: 2, Name: "Agente"}, {ID: 3, Name: "Otro"}},
		Incidents: []*Incident{i1, i2, i3, i4, i5, i6, i7},
	}
}

func TestComputeSLA(t *testing.T) {
	m := slaTenant().ComputeSLA(30, now)

	if m.Total.Incidents != 6 {
		t.Fatalf("el período tiene 6 incidentes (el de febrero queda afuera), obtuve %d", m.Total.Incidents)
	}
	// Resolución: #1 en plazo y #2 tarde son los únicos medibles; #3, #4 y #6 siguen en plazo; #5 no tiene SLA.
	if r := m.Total.Resolution; r.Measured != 2 || r.Late != 1 || !near(r.Compliance, 50) {
		t.Errorf("resolución: %+v", r)
	}
	// Primera respuesta: #1 en plazo, #2 tarde y #3 sin responder y vencida. #4 todavía está en plazo.
	if r := m.Total.Response; r.Measured != 3 || r.Late != 2 || !near(r.Compliance, 33.3) {
		t.Errorf("primera respuesta: %+v", r)
	}
	// Tiempos medios: resolución de #1 (6 h), #2 (12 h) y #5 (1 h); respuesta de #1 (1 h) y #2 (5 h).
	if !near(m.Total.AvgResolutionHours, 6.3) || !near(m.Total.AvgResponseHours, 3) {
		t.Errorf("tiempos medios: %v h resolución, %v h respuesta", *m.Total.AvgResolutionHours, *m.Total.AvgResponseHours)
	}

	if len(m.ByPriority) != 3 || m.ByPriority[0].Name != "alta" || m.ByPriority[0].Incidents != 4 || m.ByPriority[1].Name != "media" || m.ByPriority[2].Name != "baja" {
		t.Errorf("por prioridad (en el orden alta, media, baja): %+v", m.ByPriority)
	}
	if r := m.ByPriority[0].Resolution; r.Measured != 2 || !near(r.Compliance, 50) {
		t.Errorf("resolución de las altas: %+v", r)
	}
	if r := m.ByPriority[2].Resolution; r.Measured != 0 || r.Compliance != nil {
		t.Errorf("las bajas no tienen nada medible y no deben mostrar un 0 %%: %+v", r)
	}

	// Por servicio y por responsable, de más a menos incidentes.
	if len(m.ByService) != 2 || m.ByService[0].Name != "VPN" || m.ByService[0].Incidents != 5 || m.ByService[1].Name != "Correo" {
		t.Errorf("por servicio: %+v", m.ByService)
	}
	names := []string{}
	for _, g := range m.ByAssignee {
		names = append(names, g.Name)
	}
	if len(names) != 3 || names[0] != "Sin asignar" || names[1] != "Agente" || names[2] != "Otro" {
		t.Errorf("por responsable: %v", names)
	}
	if g := m.ByAssignee[1]; g.ID != 2 || g.Incidents != 2 || !near(g.Resolution.Compliance, 50) {
		t.Errorf("el agente: %+v", g)
	}
}

// La tendencia reparte los incidentes del período en tramos iguales según cuándo se crearon.
func TestComputeSLATrend(t *testing.T) {
	m := slaTenant().ComputeSLA(30, now)
	if len(m.Trend) != 6 {
		t.Fatalf("30 días se dividen en 6 tramos, obtuve %d", len(m.Trend))
	}
	sum := 0
	for k, p := range m.Trend {
		sum += p.Incidents
		if k > 0 && !p.From.Equal(m.Trend[k-1].To) {
			t.Errorf("el tramo %d no empieza donde termina el anterior", k)
		}
	}
	if sum != m.Total.Incidents {
		t.Errorf("los tramos suman %d y el total es %d", sum, m.Total.Incidents)
	}
	if last := m.Trend[5]; last.Incidents != 6 || !near(last.Resolution.Compliance, 50) {
		t.Errorf("el último tramo tiene todos los incidentes: %+v", last)
	}
	if !m.Trend[5].To.Equal(now) {
		t.Errorf("el último tramo termina ahora: %v", m.Trend[5].To)
	}
	if got := len(slaTenant().ComputeSLA(7, now).Trend); got != 7 {
		t.Errorf("7 días se dividen en 7 tramos, obtuve %d", got)
	}
}

// Por vencer: los abiertos que ya vencieron o vencen en las próximas horas. En pausa el reloj no corre.
func TestComputeSLAAtRisk(t *testing.T) {
	m := slaTenant().ComputeSLA(30, now)
	if len(m.AtRisk) != 2 {
		t.Fatalf("esperaba 2 avisos (#3 respuesta vencida y #3 resolución por vencer), obtuve %+v", m.AtRisk)
	}
	first, second := m.AtRisk[0], m.AtRisk[1]
	if first.ID != 3 || first.Kind != "respuesta" || !first.Late {
		t.Errorf("primero (el que venció antes): %+v", first)
	}
	if second.ID != 3 || second.Kind != "resolucion" || second.Late {
		t.Errorf("segundo (vence 15:00, todavía en plazo): %+v", second)
	}
	for _, r := range m.AtRisk {
		if r.ID == 6 || r.ID == 4 {
			t.Errorf("#%d no debería figurar: está en pausa o lejos del vencimiento", r.ID)
		}
	}
}

// Una cuenta sin incidentes devuelve listas vacías (no null) y sin porcentajes inventados.
func TestComputeSLAEmpty(t *testing.T) {
	m := (&Tenant{}).ComputeSLA(7, now)
	if m.ByPriority == nil || m.ByService == nil || m.ByAssignee == nil || m.AtRisk == nil || len(m.Trend) != 7 {
		t.Fatalf("las listas tienen que existir: %+v", m)
	}
	if m.Total.Resolution.Compliance != nil || m.Total.AvgResolutionHours != nil {
		t.Errorf("sin datos no hay porcentaje ni promedio: %+v", m.Total)
	}
}

func TestSLALate(t *testing.T) {
	inc := &Incident{DueAt: at(30, 14), PausedAt: at(30, 12)}
	if inc.ResolutionLate(now) {
		t.Error("en pausa el reloj quedó detenido a las 12, antes del vencimiento")
	}
	inc.PausedAt = nil
	if !inc.ResolutionLate(now) {
		t.Error("abierto y pasado el vencimiento está fuera de plazo")
	}
	if (&Incident{}).ResolutionLate(now) || (&Incident{}).ResponseLate(now) {
		t.Error("sin vencimiento nunca está fuera de plazo")
	}
}
