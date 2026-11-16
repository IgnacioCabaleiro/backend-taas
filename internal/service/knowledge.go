package service

import (
	"strings"

	"taas-backend/internal/domain"
)

// Knowledge sugiere errores conocidos e incidentes parecidos a partir del texto de un ticket.
type Knowledge struct{ base }

// ProblemHit: un problema que se parece a lo que alguien está describiendo.
type ProblemHit struct {
	ProblemID  int      `json:"problemId"`
	Title      string   `json:"title"`
	Service    string   `json:"service"`
	Status     string   `json:"status"`
	Workaround string   `json:"workaround"` // vacío si no tiene o si quien pregunta no puede verlo
	Score      float64  `json:"score"`
	Shared     []string `json:"shared"` // palabras en común
}

// IncidentHit: un incidente parecido a otro (posible duplicado).
type IncidentHit struct {
	IncidentID int      `json:"incidentId"`
	Title      string   `json:"title"`
	Status     string   `json:"status"`
	Resolved   bool     `json:"resolved"`
	ProblemID  int      `json:"problemId"`
	Score      float64  `json:"score"`
	Shared     []string `json:"shared"`
}

// Related: lo que se parece a un incidente ya registrado.
type Related struct {
	Problems  []ProblemHit  `json:"problems"`
	Incidents []IncidentHit `json:"incidents"`
}

const matchLimit = 3

// read toma la cuenta con el lock, para las consultas que no modifican nada.
func (b base) read(tenantID int, fn func(t *domain.Tenant) error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, err := b.tenants.Get(tenantID)
	if err != nil {
		return err
	}
	return fn(t)
}

func problemHits(t *domain.Tenant, matches []domain.Match, showWorkaround bool) []ProblemHit {
	out := []ProblemHit{}
	for _, m := range matches {
		p, _ := t.Problem(m.ID)
		h := ProblemHit{ProblemID: p.ID, Title: p.Title, Service: p.Service, Status: p.Status, Score: m.Score, Shared: m.Shared}
		if p.Status == domain.ErrorConocido && (showWorkaround || p.WorkaroundShared) {
			h.Workaround = p.Workaround
		}
		out = append(out, h)
	}
	return out
}

// Match busca errores conocidos parecidos a un incidente que todavía se está escribiendo.
// Quien atiende ve todos los problemas sin resolver; quien solo reporta, solo los errores conocidos
// con un workaround compartido, para que pueda resolverlo sin abrir un ticket.
func (s *Knowledge) Match(tenantID, by int, text, service string) ([]ProblemHit, error) {
	var out []ProblemHit
	err := s.read(tenantID, func(t *domain.Tenant) error {
		if !t.Can(by, domain.PermCreate) && !t.SeesAll(by) {
			return domain.ErrForbidden
		}
		out = []ProblemHit{}
		if !t.Config.Modules.Problems || len(strings.TrimSpace(text)) < 4 {
			return nil
		}
		team := t.SeesAll(by)
		out = problemHits(t, t.MatchProblems(text, service, !team, matchLimit), team)
		return nil
	})
	return out, err
}

// Related busca, para un incidente ya registrado, los errores conocidos y los incidentes abiertos que se le parecen.
// Es una herramienta de quien atiende.
func (s *Knowledge) Related(tenantID, by, id int) (Related, error) {
	var out Related
	err := s.read(tenantID, func(t *domain.Tenant) error {
		if !t.SeesAll(by) {
			return domain.ErrForbidden
		}
		inc, err := t.Incident(id)
		if err != nil {
			return err
		}
		out = Related{Problems: []ProblemHit{}, Incidents: []IncidentHit{}}
		if t.Config.Modules.Problems {
			for _, h := range problemHits(t, t.MatchProblems(inc.Title+" "+inc.Description, inc.Service, false, matchLimit+1), true) {
				if h.ProblemID != inc.ProblemID && len(out.Problems) < matchLimit {
					out.Problems = append(out.Problems, h)
				}
			}
		}
		for _, m := range t.SimilarIncidents(inc, matchLimit+2) {
			o, _ := t.Incident(m.ID)
			out.Incidents = append(out.Incidents, IncidentHit{IncidentID: o.ID, Title: o.Title, Status: o.Status,
				Resolved: o.ResolvedAt != nil, ProblemID: o.ProblemID, Score: m.Score, Shared: m.Shared})
		}
		return nil
	})
	return out, err
}
