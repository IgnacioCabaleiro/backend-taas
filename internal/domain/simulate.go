package domain

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// Simulador de SLA: qué hubiera pasado con otros plazos, sobre los incidentes reales de un período.
//
// El vencimiento guardado de cada incidente ya incluye lo que estuvo en pausa. Para no perder eso, se recupera
// el plazo con el que se registró (de la versión de la configuración vigente en ese momento) y se corre la
// diferencia: vencimiento simulado = registro + plazo nuevo + lo que estuvo en pausa.

type SimInput struct {
	Days          int            `json:"days"`
	SLAHours      map[string]int `json:"slaHours"`
	ResponseHours map[string]int `json:"responseHours"`
}

// SimStat compara el cumplimiento real con el simulado. Measured: incidentes medibles (resueltos o ya vencidos).
type SimStat struct {
	Measured int `json:"measured"`
	Late     int `json:"late"`
	SimLate  int `json:"simLate"`
}

type SimRow struct {
	Priority   string  `json:"priority"`
	Resolution SimStat `json:"resolution"`
	Response   SimStat `json:"response"`
}

// SimChange: un incidente que cambia de lado (estaba en plazo y quedaría fuera, o al revés).
type SimChange struct {
	ID       int    `json:"id"`
	Title    string `json:"title"`
	Priority string `json:"priority"`
	Kind     string `json:"kind"` // resolucion | respuesta
	WasLate  bool   `json:"wasLate"`
}

type SimResult struct {
	Days    int         `json:"days"`
	Total   SimRow      `json:"total"`
	Rows    []SimRow    `json:"rows"`
	Changes []SimChange `json:"changes"`
}

// configAt devuelve la configuración vigente en un momento según el historial de versiones
// (la primera, si el momento es anterior a todas; la actual, si no hay historial).
func (t *Tenant) configAt(at time.Time) Config {
	c := t.Config
	for i, v := range t.ConfigVersions {
		if i == 0 || !v.At.After(at) {
			c = v.Config
		}
	}
	return c
}

func validHours(m map[string]int, what string) error {
	for _, p := range Priorities {
		if m[p] < 1 {
			return fmt.Errorf("%s de prioridad %s tiene que ser de al menos 1 hora", what, p)
		}
	}
	return nil
}

func (s *SimStat) add(measured, late, simLate bool) {
	if !measured {
		return
	}
	s.Measured++
	if late {
		s.Late++
	}
	if simLate {
		s.SimLate++
	}
}

func (r *SimRow) merge(o SimRow) {
	for _, p := range []struct{ a, b *SimStat }{{&r.Resolution, &o.Resolution}, {&r.Response, &o.Response}} {
		p.a.Measured += p.b.Measured
		p.a.Late += p.b.Late
		p.a.SimLate += p.b.SimLate
	}
}

// SimulateSLA recalcula el cumplimiento de los incidentes de los últimos in.Days días con los plazos de in.
func (t *Tenant) SimulateSLA(in SimInput, now time.Time) (SimResult, error) {
	if _, ok := SLAPeriods[in.Days]; !ok {
		return SimResult{}, errors.New("el período tiene que ser de 7, 30 o 90 días")
	}
	if err := validHours(in.SLAHours, "la resolución"); err != nil {
		return SimResult{}, err
	}
	if err := validHours(in.ResponseHours, "la primera respuesta"); err != nil {
		return SimResult{}, err
	}
	start := now.Add(-time.Duration(in.Days) * 24 * time.Hour)
	rows := map[string]*SimRow{}
	res := SimResult{Days: in.Days, Total: SimRow{Priority: "total"}, Rows: []SimRow{}, Changes: []SimChange{}}
	hours := func(h int) time.Duration { return time.Duration(h) * time.Hour }

	// El simulador cambia las horas por prioridad: las solicitudes no tienen prioridad (su plazo es el del catálogo).
	for _, inc := range t.Incidents {
		if inc.IsRequest() || inc.CreatedAt.Before(start) || inc.CreatedAt.After(now) {
			continue
		}
		row := rows[inc.Priority]
		if row == nil {
			row = &SimRow{Priority: inc.Priority}
			rows[inc.Priority] = row
		}
		if inc.DueAt != nil {
			was := t.configAt(inc.CreatedAt).SLAHours[inc.Priority]
			paused := inc.DueAt.Sub(inc.CreatedAt) - hours(was)
			if paused < 0 {
				paused = 0
			}
			due := inc.CreatedAt.Add(hours(in.SLAHours[inc.Priority]) + paused)
			ref := now
			switch {
			case inc.ResolvedAt != nil:
				ref = *inc.ResolvedAt
			case inc.PausedAt != nil:
				ref = *inc.PausedAt
			}
			late, simLate := inc.ResolutionLate(now), ref.After(due)
			row.Resolution.add(inc.ResolvedAt != nil || late || simLate, late, simLate)
			if late != simLate {
				res.Changes = append(res.Changes, SimChange{inc.ID, inc.Title, inc.Priority, "resolucion", late})
			}
		}
		if inc.ResponseDueAt != nil {
			due := inc.CreatedAt.Add(hours(in.ResponseHours[inc.Priority]))
			ref := now
			if inc.FirstResponseAt != nil {
				ref = *inc.FirstResponseAt
			}
			late, simLate := inc.ResponseLate(now), ref.After(due)
			row.Response.add(inc.FirstResponseAt != nil || late || simLate, late, simLate)
			if late != simLate {
				res.Changes = append(res.Changes, SimChange{inc.ID, inc.Title, inc.Priority, "respuesta", late})
			}
		}
	}
	for _, p := range Priorities {
		if r := rows[p]; r != nil {
			res.Rows = append(res.Rows, *r)
			res.Total.merge(*r)
		}
	}
	sort.SliceStable(res.Changes, func(i, j int) bool { return res.Changes[i].ID > res.Changes[j].ID })
	return res, nil
}
