package service

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"taas-backend/internal/domain"
)

type Problems struct{ base }

// update de problemas: además del permiso, la cuenta tiene que tener el módulo activo.
func (s *Problems) update(tenantID, by int, fn func(t *domain.Tenant) error) error {
	return s.base.update(tenantID, by, domain.PermProblems, func(t *domain.Tenant) error {
		if !t.Config.Modules.Problems {
			return errors.New("la gestión de problemas no está activada en esta cuenta")
		}
		return fn(t)
	})
}

func (s *Problems) Create(tenantID, by int, in domain.NewProblem) (p *domain.Problem, err error) {
	err = s.update(tenantID, by, func(t *domain.Tenant) error {
		title := strings.TrimSpace(in.Title)
		if title == "" {
			return errors.New("ponele un título al problema")
		}
		if !domain.Has(t.Config.Services, in.Service) {
			return errors.New("elegí un servicio del catálogo")
		}
		for _, id := range in.IncidentIDs {
			if _, err := t.Incident(id); err != nil {
				return err
			}
		}
		p = &domain.Problem{ID: len(t.Problems) + 1, Title: title, Description: in.Description, Service: in.Service,
			Status: domain.Identificado, OwnerID: by, CreatedAt: time.Now()}
		if len(in.IncidentIDs) > 0 {
			p.Log(by, "", "Problema creado desde %d incidentes recurrentes", len(in.IncidentIDs))
		} else {
			p.Log(by, "", "Problema creado manualmente")
		}
		t.Problems = append(t.Problems, p)
		for _, id := range in.IncidentIDs {
			t.Incidents[id-1].ProblemID = p.ID
		}
		return nil
	})
	return p, err
}

// Advance mueve el problema al siguiente estado, exigiendo lo que cada etapa necesita.
func (s *Problems) Advance(tenantID, by, id int, in domain.Advance) error {
	return s.update(tenantID, by, func(t *domain.Tenant) error {
		p, err := t.Problem(id)
		if err != nil {
			return err
		}
		switch p.Status {
		case domain.Identificado:
			p.Status = domain.EnAnalisis
			p.Log(by, "analisis", "Análisis de causa raíz iniciado")
		case domain.EnAnalisis:
			if strings.TrimSpace(in.RootCause) == "" {
				return errors.New("para registrar un error conocido hay que documentar la causa raíz")
			}
			p.RootCause, p.Workaround = in.RootCause, in.Workaround
			p.Status = domain.ErrorConocido
			p.Log(by, "conocido", "Registrado como error conocido")
		case domain.ErrorConocido:
			if strings.TrimSpace(in.Solution) == "" {
				return errors.New("falta la solución definitiva")
			}
			p.Solution, p.Status = in.Solution, domain.Resuelto
			closed := 0
			for _, inc := range t.Incidents {
				if inc.ProblemID == p.ID && inc.ResolvedAt == nil {
					now := time.Now()
					inc.Status, inc.ResolvedAt = t.ClosedState(), &now
					closed++
				}
			}
			p.Log(by, "resuelto", "Resuelto · %d incidentes cerrados", closed)
		default:
			return errors.New("el problema ya está resuelto")
		}
		return nil
	})
}

func (s *Problems) SetOwner(tenantID, by, id, ownerID int) error {
	return s.update(tenantID, by, func(t *domain.Tenant) error {
		p, err := t.Problem(id)
		if err != nil {
			return err
		}
		u := t.User(ownerID)
		if u == nil {
			return fmt.Errorf("usuario %d: %w", ownerID, domain.ErrNotFound)
		}
		p.OwnerID = ownerID
		p.Log(by, "", "Responsable: %s", u.Name)
		return nil
	})
}

func (s *Problems) Comment(tenantID, by, id int, text string) error {
	return s.update(tenantID, by, func(t *domain.Tenant) error {
		p, err := t.Problem(id)
		if err != nil {
			return err
		}
		if text = strings.TrimSpace(text); text == "" {
			return errors.New("el comentario está vacío")
		}
		p.Log(by, "comentario", "%s", text)
		return nil
	})
}

// Suggestions agrupa por servicio los incidentes sin problema de los últimos RecurrenceDays días
// (abiertos o no: que se hayan resuelto uno por uno es justamente la señal de un problema de fondo).
func Suggestions(t *domain.Tenant) []domain.Suggestion {
	out := []domain.Suggestion{}
	if !t.Config.Modules.Problems {
		return out
	}
	since := time.Now().AddDate(0, 0, -t.Config.RecurrenceDays)
	by := map[string][]int{}
	for _, inc := range t.Incidents {
		if inc.ProblemID == 0 && inc.CreatedAt.After(since) {
			by[inc.Service] = append(by[inc.Service], inc.ID)
		}
	}
	for svc, ids := range by {
		if len(ids) >= t.Config.RecurrenceMin {
			out = append(out, domain.Suggestion{Service: svc, IncidentIDs: ids})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].IncidentIDs) != len(out[j].IncidentIDs) {
			return len(out[i].IncidentIDs) > len(out[j].IncidentIDs)
		}
		return out[i].Service < out[j].Service
	})
	return out
}
