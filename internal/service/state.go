package service

import (
	"fmt"
	"time"

	"taas-backend/internal/domain"
)

// State arma lo que ve un usuario de su cuenta.
type State struct{ base }

// View es la cuenta tal como la ve el usuario logueado: sin contraseñas y recortada según sus permisos.
type View struct {
	ID          int                 `json:"id"`
	Name        string              `json:"name"`
	Industry    string              `json:"industry"`
	Onboarded   bool                `json:"onboarded"`
	OwnerID     int                 `json:"ownerId"`
	Me          int                 `json:"me"`
	MaxUsers    int                 `json:"maxUsers"`
	Config      domain.Config       `json:"config"`
	Roles       []domain.Role       `json:"roles"`
	Users       []domain.User       `json:"users"`
	Incidents   []*domain.Incident  `json:"incidents"`
	Problems    []*domain.Problem   `json:"problems"`
	Suggestions []domain.Suggestion `json:"suggestions"`
}

func (s *State) View(tenantID, me int) (View, error) {
	s.mu.Lock()
	t, err := s.tenants.Get(tenantID)
	if err == nil && t.AutoClose(time.Now()) > 0 { // barrido de cierre automático (ver base.modify)
		if err = s.tenants.Save(t); err != nil {
			err = fmt.Errorf("guardar la cuenta %d: %v: %w", t.ID, err, domain.ErrInternal)
		}
	}
	if err == nil {
		t = t.Clone() // la vista se usa fuera del lock
	}
	s.mu.Unlock()
	if err != nil {
		return View{}, err
	}
	v := View{ID: t.ID, Name: t.Name, Industry: t.Industry, Onboarded: t.Onboarded, OwnerID: t.OwnerID, Me: me,
		MaxUsers: domain.MaxUsers, Config: t.Config, Roles: t.Roles,
		Incidents: []*domain.Incident{}, Problems: []*domain.Problem{}, Suggestions: []domain.Suggestion{}}
	for _, u := range t.Users {
		u.Hash = ""
		v.Users = append(v.Users, u)
	}
	// Quien solo reporta ve sus propios incidentes. Para ver todos hace falta resolver tickets, ver el panel
	// o gestionar problemas (las sugerencias y los problemas apuntan a incidentes de cualquiera).
	// Quien solo reporta tampoco ve las notas internas de trabajo ni los vínculos con problemas (los problemas
	// son internos del equipo): de la comunicación, solo las respuestas.
	all := t.Can(me, domain.PermResolve) || t.Can(me, domain.PermPanel) || t.Can(me, domain.PermProblems)
	for _, inc := range t.Incidents {
		switch {
		case all:
			v.Incidents = append(v.Incidents, inc)
		case inc.CreatedBy == me:
			v.Incidents = append(v.Incidents, forReporter(inc))
		}
	}
	if t.Config.Modules.Problems && all {
		v.Problems = t.Problems
	}
	if t.Can(me, domain.PermProblems) {
		v.Suggestions = Suggestions(t)
	}
	return v, nil
}

// forReporter devuelve una copia del incidente para quien solo reporta: sin las notas internas ni los eventos
// de problema. La vista no toca la cuenta.
func forReporter(inc *domain.Incident) *domain.Incident {
	c := *inc
	c.History = []domain.Event{}
	for _, e := range inc.History {
		if e.Kind != domain.EvNote && e.Kind != domain.EvProblem {
			c.History = append(c.History, e)
		}
	}
	return &c
}
