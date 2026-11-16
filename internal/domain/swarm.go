package domain

import (
	"sort"
	"time"
)

// Swarming: en lugar de escalar un problema de nivel en nivel (N1, N2, N3), lo investiga todo el equipo
// a la vez. Con cuentas de pocas personas es la forma natural de trabajar: quien sabe algo, lo suma.
type Swarm struct {
	StartedAt time.Time   `json:"startedAt"`
	StartedBy int         `json:"startedBy"`
	ClosedAt  *time.Time  `json:"closedAt,omitempty"`
	Members   []int       `json:"members"`
	Notes     []SwarmNote `json:"notes"`
}

// SwarmNote: un aporte al swarming. Kind dice qué tan firme es: una pista para seguir, algo que ya se descartó,
// o un hallazgo (lo que se confirmó y probablemente sea la causa).
type SwarmNote struct {
	At     time.Time `json:"at"`
	UserID int       `json:"userId"`
	Kind   string    `json:"kind"` // pista | descartado | hallazgo
	Text   string    `json:"text"`
}

var SwarmKinds = []string{"pista", "descartado", "hallazgo"}

// Active: el swarming está en curso.
func (s *Swarm) Active() bool { return s != nil && s.ClosedAt == nil }

// Join suma a alguien al swarming (una sola vez).
func (s *Swarm) Join(userID int) bool {
	for _, m := range s.Members {
		if m == userID {
			return false
		}
	}
	s.Members = append(s.Members, userID)
	return true
}

// CrossSuggestion: el mismo síntoma en incidentes de servicios distintos que nadie investigó todavía.
// Suele ser una causa compartida que no aparece mirando servicio por servicio (un corte de luz, la red de un piso).
type CrossSuggestion struct {
	Terms       []string `json:"terms"` // las palabras que tienen en común
	IncidentIDs []int    `json:"incidentIds"`
	Services    []string `json:"services"`
}

// MinCrossScore: qué tan parecidos tienen que ser dos incidentes de distinto servicio para agruparlos.
const MinCrossScore = 0.3

// CrossServiceRecurrence agrupa por similitud de texto los incidentes sin problema de los últimos
// RecurrenceDays días, y devuelve los grupos que tocan más de un servicio.
func (t *Tenant) CrossServiceRecurrence(now time.Time) []CrossSuggestion {
	out := []CrossSuggestion{}
	if !t.Config.Modules.Problems {
		return out
	}
	since := now.AddDate(0, 0, -t.Config.RecurrenceDays)
	var pool []*Incident
	docs := map[int]string{}
	for _, inc := range t.Incidents {
		// Las solicitudes no entran: no tienen servicio y no son fallas (pedir muchas altas no es un síntoma común).
		if !inc.IsRequest() && inc.ProblemID == 0 && inc.CreatedAt.After(since) {
			pool = append(pool, inc)
			docs[inc.ID] = inc.Title + " " + inc.Description
		}
	}
	// Unión de los pares parecidos: cada grupo queda con su raíz.
	parent := map[int]int{}
	var root func(int) int
	root = func(id int) int {
		if p, ok := parent[id]; ok && p != id {
			parent[id] = root(p)
			return parent[id]
		}
		parent[id] = id
		return id
	}
	terms := map[[2]int][]string{}
	for _, inc := range pool {
		for _, m := range rank(docs[inc.ID], docs, nil, len(docs)) {
			o, _ := t.Incident(m.ID)
			if o.ID == inc.ID || o.Service == inc.Service || m.Score < MinCrossScore {
				continue
			}
			parent[root(o.ID)] = root(inc.ID)
			terms[[2]int{inc.ID, o.ID}] = m.Shared
		}
	}
	groups := map[int][]*Incident{}
	for _, inc := range pool {
		if _, ok := parent[inc.ID]; ok {
			groups[root(inc.ID)] = append(groups[root(inc.ID)], inc)
		}
	}
	for _, g := range groups {
		services := []string{}
		ids := []int{}
		for _, inc := range g {
			ids = append(ids, inc.ID)
			if !Has(services, inc.Service) {
				services = append(services, inc.Service)
			}
		}
		if len(g) < t.Config.RecurrenceMin || len(services) < 2 {
			continue
		}
		// Las palabras que más se repiten entre los pares del grupo.
		freq := map[string]int{}
		for pair, ws := range terms {
			if root(pair[0]) == root(g[0].ID) {
				for _, w := range ws {
					freq[w]++
				}
			}
		}
		words := []string{}
		for w := range freq {
			words = append(words, w)
		}
		sort.Slice(words, func(i, j int) bool {
			if freq[words[i]] != freq[words[j]] {
				return freq[words[i]] > freq[words[j]]
			}
			return words[i] < words[j]
		})
		if len(words) > 4 {
			words = words[:4]
		}
		sort.Ints(ids)
		out = append(out, CrossSuggestion{Terms: words, IncidentIDs: ids, Services: services})
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i].IncidentIDs) > len(out[j].IncidentIDs) })
	return out
}
