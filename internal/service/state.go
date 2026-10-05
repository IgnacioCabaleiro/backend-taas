package service

import (
	"taas-backend/internal/domain"
	"taas-backend/internal/repository"
)

// State arma lo que ve un usuario de su cuenta.
type State struct{ tenants repository.Tenants }

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
	t, err := s.tenants.Get(tenantID)
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
	all := t.Can(me, domain.PermResolve) || t.Can(me, domain.PermPanel) || t.Can(me, domain.PermProblems)
	for _, inc := range t.Incidents {
		if all || inc.CreatedBy == me {
			v.Incidents = append(v.Incidents, inc)
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
