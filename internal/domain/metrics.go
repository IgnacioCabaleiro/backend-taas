package domain

import (
	"math"
	"sort"
	"time"
)

// Métricas de SLA de una cuenta: cumplimiento de la primera respuesta y de la resolución, con su desglose,
// la tendencia y lo que está por vencer. Se calculan sobre los incidentes creados en el período.
//
// Un incidente se puede medir (Measured) cuando ya se sabe si cumplió: se resolvió (o respondió), o sigue abierto
// y ya venció. Un abierto que todavía está en plazo no cuenta ni a favor ni en contra. Los incidentes registrados
// sin el módulo SLA no tienen vencimientos y no entran en el cumplimiento (sí en los tiempos medios).

// SLAPeriods: los períodos que se pueden pedir (en días) y en cuántos tramos iguales se divide la tendencia.
var SLAPeriods = map[int]int{7: 7, 30: 6, 90: 9}

// SLAAtRiskHours: un abierto cuyo vencimiento cae dentro de estas horas se marca "por vencer".
const SLAAtRiskHours = 4

type SLAStat struct {
	Measured   int      `json:"measured"`
	Late       int      `json:"late"`
	Compliance *float64 `json:"compliance"` // 0 a 100; nil si todavía no hay nada que medir
}

// SLAGroup: las métricas de un grupo de incidentes (una prioridad, un servicio o quien los tiene asignados).
type SLAGroup struct {
	ID                 int      `json:"id"` // en el desglose por responsable, el usuario (0 = sin asignar)
	Name               string   `json:"name"`
	Incidents          int      `json:"incidents"`
	Resolution         SLAStat  `json:"resolution"`
	Response           SLAStat  `json:"response"`
	AvgResolutionHours *float64 `json:"avgResolutionHours"` // creación a resolución, de los resueltos
	AvgResponseHours   *float64 `json:"avgResponseHours"`   // creación a primera respuesta, de los respondidos
}

// SLATrendPoint: los incidentes creados en un tramo del período.
type SLATrendPoint struct {
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`
	Incidents  int       `json:"incidents"`
	Resolution SLAStat   `json:"resolution"`
	Response   SLAStat   `json:"response"`
}

// SLARisk: un incidente abierto que ya venció o está por vencer. Kind: "resolucion" o "respuesta".
type SLARisk struct {
	ID         int       `json:"id"`
	Title      string    `json:"title"`
	Service    string    `json:"service"`
	Priority   string    `json:"priority"`
	AssigneeID int       `json:"assigneeId"`
	Kind       string    `json:"kind"`
	DueAt      time.Time `json:"dueAt"`
	Late       bool      `json:"late"`
}

type SLAMetrics struct {
	Days        int             `json:"days"`
	GeneratedAt time.Time       `json:"generatedAt"`
	Total       SLAGroup        `json:"total"`
	ByPriority  []SLAGroup      `json:"byPriority"`
	ByService   []SLAGroup      `json:"byService"`
	ByAssignee  []SLAGroup      `json:"byAssignee"`
	Trend       []SLATrendPoint `json:"trend"`
	AtRisk      []SLARisk       `json:"atRisk"` // no depende del período: es lo que pasa ahora
}

// ResolutionLate: el SLA de resolución está incumplido. Un resuelto cuenta hasta su resolución; uno en pausa,
// hasta que se pausó (DueAt ya descuenta las pausas anteriores). Sin vencimiento nunca está fuera de plazo.
func (inc *Incident) ResolutionLate(now time.Time) bool {
	if inc.DueAt == nil {
		return false
	}
	ref := now
	switch {
	case inc.ResolvedAt != nil:
		ref = *inc.ResolvedAt
	case inc.PausedAt != nil:
		ref = *inc.PausedAt
	}
	return ref.After(*inc.DueAt)
}

// ResponseLate: el SLA de la primera respuesta está incumplido. La pausa no lo afecta.
func (inc *Incident) ResponseLate(now time.Time) bool {
	if inc.ResponseDueAt == nil {
		return false
	}
	ref := now
	if inc.FirstResponseAt != nil {
		ref = *inc.FirstResponseAt
	}
	return ref.After(*inc.ResponseDueAt)
}

// slaAcc acumula las métricas de un conjunto de incidentes.
type slaAcc struct {
	n                    int
	resolution, response SLAStat
	resolutionH          []float64
	responseH            []float64
}

func (a *slaAcc) add(inc *Incident, now time.Time) {
	a.n++
	if inc.DueAt != nil {
		if late := inc.ResolutionLate(now); late || inc.ResolvedAt != nil {
			a.resolution.Measured++
			if late {
				a.resolution.Late++
			}
		}
	}
	if inc.ResponseDueAt != nil {
		if late := inc.ResponseLate(now); late || inc.FirstResponseAt != nil {
			a.response.Measured++
			if late {
				a.response.Late++
			}
		}
	}
	if inc.ResolvedAt != nil {
		a.resolutionH = append(a.resolutionH, inc.ResolvedAt.Sub(inc.CreatedAt).Hours())
	}
	if inc.FirstResponseAt != nil {
		a.responseH = append(a.responseH, inc.FirstResponseAt.Sub(inc.CreatedAt).Hours())
	}
}

func (a *slaAcc) group(id int, name string) SLAGroup {
	return SLAGroup{ID: id, Name: name, Incidents: a.n, Resolution: a.resolution.done(), Response: a.response.done(),
		AvgResolutionHours: mean(a.resolutionH), AvgResponseHours: mean(a.responseH)}
}

// done completa el porcentaje de cumplimiento.
func (s SLAStat) done() SLAStat {
	if s.Measured > 0 {
		s.Compliance = round1(100 * float64(s.Measured-s.Late) / float64(s.Measured))
	}
	return s
}

func mean(v []float64) *float64 {
	if len(v) == 0 {
		return nil
	}
	sum := 0.0
	for _, x := range v {
		sum += x
	}
	return round1(sum / float64(len(v)))
}

func round1(x float64) *float64 {
	r := math.Round(x*10) / 10
	return &r
}

func accFor[K comparable](m map[K]*slaAcc, k K) *slaAcc {
	if m[k] == nil {
		m[k] = &slaAcc{}
	}
	return m[k]
}

// ComputeSLA calcula las métricas de los incidentes creados en los últimos days días (days tiene que ser uno de
// SLAPeriods). Recibe el momento para poder probarlo con fechas fijas.
func (t *Tenant) ComputeSLA(days int, now time.Time) SLAMetrics {
	start := now.Add(-time.Duration(days) * 24 * time.Hour)
	m := SLAMetrics{Days: days, GeneratedAt: now, ByPriority: []SLAGroup{}, ByService: []SLAGroup{}, ByAssignee: []SLAGroup{},
		Trend: []SLATrendPoint{}, AtRisk: []SLARisk{}}

	var total slaAcc
	byPrio, bySvc, byUser := map[string]*slaAcc{}, map[string]*slaAcc{}, map[int]*slaAcc{}
	n := SLAPeriods[days]
	step := time.Duration(days) * 24 * time.Hour / time.Duration(n)
	buckets := make([]slaAcc, n)
	for _, inc := range t.Incidents {
		if inc.CreatedAt.Before(start) || inc.CreatedAt.After(now) {
			continue
		}
		total.add(inc, now)
		accFor(byPrio, inc.Priority).add(inc, now)
		accFor(bySvc, inc.Service).add(inc, now)
		accFor(byUser, inc.AssigneeID).add(inc, now)
		k := int(inc.CreatedAt.Sub(start) / step)
		if k >= n { // creado justo en este instante
			k = n - 1
		}
		buckets[k].add(inc, now)
	}
	m.Total = total.group(0, "Total")

	for _, p := range Priorities {
		if a := byPrio[p]; a != nil {
			m.ByPriority = append(m.ByPriority, a.group(0, p))
		}
	}
	for name, a := range bySvc {
		m.ByService = append(m.ByService, a.group(0, name))
	}
	for id, a := range byUser {
		name := "Sin asignar"
		if id != 0 {
			name = "Usuario eliminado"
			if u := t.User(id); u != nil {
				name = u.Name
			}
		}
		m.ByAssignee = append(m.ByAssignee, a.group(id, name))
	}
	for _, g := range [][]SLAGroup{m.ByService, m.ByAssignee} {
		sort.Slice(g, func(i, j int) bool {
			if g[i].Incidents != g[j].Incidents {
				return g[i].Incidents > g[j].Incidents
			}
			return g[i].Name < g[j].Name
		})
	}
	for k := range buckets {
		from := start.Add(time.Duration(k) * step)
		m.Trend = append(m.Trend, SLATrendPoint{From: from, To: from.Add(step), Incidents: buckets[k].n,
			Resolution: buckets[k].resolution.done(), Response: buckets[k].response.done()})
	}

	// Por vencer: abiertos de cualquier fecha. En pausa el reloj de resolución está detenido, así que no vence.
	horizon := now.Add(SLAAtRiskHours * time.Hour)
	for _, inc := range t.Incidents {
		if inc.ResolvedAt != nil {
			continue
		}
		risk := func(kind string, due *time.Time, late bool) {
			if due != nil && (late || !due.After(horizon)) {
				m.AtRisk = append(m.AtRisk, SLARisk{ID: inc.ID, Title: inc.Title, Service: inc.Service, Priority: inc.Priority,
					AssigneeID: inc.AssigneeID, Kind: kind, DueAt: *due, Late: late})
			}
		}
		if inc.FirstResponseAt == nil {
			risk("respuesta", inc.ResponseDueAt, inc.ResponseLate(now))
		}
		if inc.PausedAt == nil {
			risk("resolucion", inc.DueAt, inc.ResolutionLate(now))
		}
	}
	sort.Slice(m.AtRisk, func(i, j int) bool { return m.AtRisk[i].DueAt.Before(m.AtRisk[j].DueAt) })
	return m
}
