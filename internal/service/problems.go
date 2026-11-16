package service

import (
	"errors"
	"fmt"
	"slices"
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
		ids := []int{}
		for _, id := range in.IncidentIDs {
			if _, err := t.Incident(id); err != nil {
				return err
			}
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		now := time.Now()
		p = &domain.Problem{ID: len(t.Problems) + 1, Title: title, Description: in.Description, Service: in.Service,
			Status: domain.Identificado, OwnerID: by, CreatedAt: now}
		t.Problems = append(t.Problems, p)
		// Crear el problema vincula también los incidentes cerrados (por ejemplo, desde una sugerencia):
		// es la única forma de vincular un cerrado, y solo cambia su problema y su historial.
		linked := 0
		for _, id := range ids {
			inc := t.Incidents[id-1]
			if old, err := t.Problem(inc.ProblemID); err == nil {
				old.Log(by, "", "#%d pasó al problema P-%d", inc.ID, p.ID)
				inc.Log(now, by, domain.EvProblem, "Desvinculado del problema P-%d", old.ID)
			}
			inc.ProblemID = p.ID
			inc.Log(now, by, domain.EvProblem, "Vinculado al problema P-%d", p.ID)
			linked++
		}
		// El historial del problema arranca con su creación, contando los incidentes que quedaron vinculados.
		if linked > 0 {
			p.Log(by, "", "Problema creado desde %d incidentes recurrentes", linked)
		} else {
			p.Log(by, "", "Problema creado manualmente")
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
			// La solución definitiva resuelve los incidentes abiertos del problema (con la pausa cerrada, si la tenían).
			// Quedan resueltos, sin cerrar: se cierran a mano o solos, como cualquier otro. La nota es solo la solución:
			// quien solo reporta ve la resolución, y los problemas son internos del equipo.
			resolved, now := 0, time.Now()
			note := strings.TrimSpace(in.Solution)
			for _, inc := range t.Incidents {
				if inc.ProblemID == p.ID && inc.ResolvedAt == nil {
					from := inc.Status
					setStatus(t, inc, t.ResolvedState(), now)
					resolve(t, inc, from, "solucionado", note, by, now)
					resolved++
				}
			}
			p.Log(by, "resuelto", "Resuelto · %d incidentes resueltos", resolved)
		default:
			return errors.New("el problema ya está resuelto")
		}
		return nil
	})
}

// UpdateWorkaround cambia el workaround de un error conocido y si se comparte con quienes reportan.
// El workaround evoluciona mientras se busca la solución definitiva; los usos anteriores se conservan.
func (s *Problems) UpdateWorkaround(tenantID, by, id int, text string, shared bool) error {
	return s.update(tenantID, by, func(t *domain.Tenant) error {
		p, err := t.Problem(id)
		if err != nil {
			return err
		}
		if p.Status != domain.ErrorConocido {
			return errors.New("el workaround se edita mientras el problema es un error conocido")
		}
		text = strings.TrimSpace(text)
		if shared && text == "" {
			return errors.New("para compartir un workaround primero hay que escribirlo")
		}
		switch {
		case text != p.Workaround && text == "":
			p.Log(by, "", "Workaround eliminado")
		case text != p.Workaround:
			p.Log(by, "", "Workaround actualizado: %s", text)
		}
		if shared != p.WorkaroundShared {
			if shared {
				p.Log(by, "", "Workaround compartido con quienes reportan")
			} else {
				p.Log(by, "", "Workaround visible solo para el equipo")
			}
		}
		p.Workaround, p.WorkaroundShared = text, shared
		return nil
	})
}

// Deflect registra que a alguien le alcanzó un workaround compartido y no abrió el ticket.
// Lo puede hacer cualquiera que pueda crear tickets: es la otra cara de registrar uno.
func (s *Problems) Deflect(tenantID, by, id int) error {
	return s.base.update(tenantID, by, domain.PermCreate, func(t *domain.Tenant) error {
		p, err := t.Problem(id)
		if err != nil {
			return err
		}
		if !t.Config.Modules.Problems || p.Status != domain.ErrorConocido || !p.WorkaroundShared {
			return fmt.Errorf("problema %d: %w", id, domain.ErrNotFound)
		}
		p.Deflections++
		p.Log(by, "", "A %s le alcanzó el workaround y no abrió un ticket", t.User(by).Name)
		return nil
	})
}

// SaveReview guarda (o reescribe) la revisión post-problema de un problema resuelto.
func (s *Problems) SaveReview(tenantID, by, id int, in domain.Review) error {
	return s.update(tenantID, by, func(t *domain.Tenant) error {
		p, err := t.Problem(id)
		if err != nil {
			return err
		}
		if p.Status != domain.Resuelto {
			return errors.New("la revisión se hace cuando el problema está resuelto")
		}
		if in.WhatHappened = strings.TrimSpace(in.WhatHappened); in.WhatHappened == "" {
			return errors.New("contá qué pasó")
		}
		actions := []domain.ReviewAction{}
		for _, a := range in.Actions {
			if a.Text = strings.TrimSpace(a.Text); a.Text == "" {
				continue
			}
			if a.OwnerID != 0 && t.User(a.OwnerID) == nil {
				return fmt.Errorf("usuario %d: %w", a.OwnerID, domain.ErrNotFound)
			}
			if a.Due != "" {
				if _, err := time.Parse("2006-01-02", a.Due); err != nil {
					return fmt.Errorf("fecha inválida en la acción «%s»", a.Text)
				}
			}
			actions = append(actions, a)
		}
		first := p.Review == nil
		p.Review = &domain.Review{WhatHappened: in.WhatHappened, Learned: strings.TrimSpace(in.Learned), Actions: actions, At: time.Now(), UserID: by}
		if first {
			p.Log(by, "", "Revisión post-problema registrada con %d acciones", len(actions))
		} else {
			p.Log(by, "", "Revisión post-problema actualizada")
		}
		return nil
	})
}

// SetActionDone marca como hecha (o pendiente) una acción de la revisión post-problema.
func (s *Problems) SetActionDone(tenantID, by, id, index int, done bool) error {
	return s.update(tenantID, by, func(t *domain.Tenant) error {
		p, err := t.Problem(id)
		if err != nil {
			return err
		}
		if p.Review == nil || index < 0 || index >= len(p.Review.Actions) {
			return fmt.Errorf("acción %d: %w", index, domain.ErrNotFound)
		}
		a := &p.Review.Actions[index]
		if a.Done != done {
			a.Done = done
			if done {
				p.Log(by, "", "Acción hecha: %s", a.Text)
			}
		}
		return nil
	})
}

// swarm toma la cuenta y valida que quien opera pueda sumarse a un swarming: atiende incidentes o gestiona
// problemas, y la cuenta tiene el módulo de problemas.
func (s *Problems) swarm(tenantID, by, id int, fn func(t *domain.Tenant, p *domain.Problem) error) error {
	return s.modify(tenantID, func(t *domain.Tenant) error {
		if !t.Can(by, domain.PermProblems) && !t.Can(by, domain.PermResolve) {
			return domain.ErrForbidden
		}
		if !t.Config.Modules.Problems {
			return errors.New("la gestión de problemas no está activada en esta cuenta")
		}
		p, err := t.Problem(id)
		if err != nil {
			return err
		}
		return fn(t, p)
	})
}

// StartSwarm abre un swarming: el problema lo investiga todo el equipo a la vez, en lugar de escalarlo.
func (s *Problems) StartSwarm(tenantID, by, id int) error {
	return s.swarm(tenantID, by, id, func(t *domain.Tenant, p *domain.Problem) error {
		if p.Status == domain.Resuelto {
			return errors.New("el problema ya está resuelto")
		}
		if p.Swarm.Active() {
			return errors.New("ya hay un swarming en curso")
		}
		p.Swarm = &domain.Swarm{StartedAt: time.Now(), StartedBy: by, Members: []int{by}, Notes: []domain.SwarmNote{}}
		p.Log(by, "", "%s abrió un swarming: el equipo investiga en conjunto", t.User(by).Name)
		return nil
	})
}

// SwarmNote suma un aporte al swarming en curso (y a quien lo hace, como participante).
func (s *Problems) SwarmNote(tenantID, by, id int, kind, text string) error {
	return s.swarm(tenantID, by, id, func(t *domain.Tenant, p *domain.Problem) error {
		if !p.Swarm.Active() {
			return errors.New("no hay un swarming en curso")
		}
		if !domain.Has(domain.SwarmKinds, kind) {
			return errors.New("elegí si es una pista, algo descartado o un hallazgo")
		}
		if text = strings.TrimSpace(text); text == "" {
			return errors.New("el aporte está vacío")
		}
		p.Swarm.Join(by)
		p.Swarm.Notes = append(p.Swarm.Notes, domain.SwarmNote{At: time.Now(), UserID: by, Kind: kind, Text: text})
		return nil
	})
}

// JoinSwarm suma a alguien al swarming en curso.
func (s *Problems) JoinSwarm(tenantID, by, id int) error {
	return s.swarm(tenantID, by, id, func(t *domain.Tenant, p *domain.Problem) error {
		if !p.Swarm.Active() {
			return errors.New("no hay un swarming en curso")
		}
		p.Swarm.Join(by)
		return nil
	})
}

// CloseSwarm cierra el swarming en curso; sus aportes quedan en el problema.
func (s *Problems) CloseSwarm(tenantID, by, id int) error {
	return s.swarm(tenantID, by, id, func(t *domain.Tenant, p *domain.Problem) error {
		if !p.Swarm.Active() {
			return errors.New("no hay un swarming en curso")
		}
		now := time.Now()
		p.Swarm.ClosedAt = &now
		p.Log(by, "", "Swarming cerrado: %d aportes de %d personas", len(p.Swarm.Notes), len(p.Swarm.Members))
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
		if !t.Can(ownerID, domain.PermProblems) {
			return errors.New("el responsable de un problema tiene que poder gestionar problemas")
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
