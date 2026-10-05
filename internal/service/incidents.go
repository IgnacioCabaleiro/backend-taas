package service

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"taas-backend/internal/domain"
)

type Incidents struct{ base }

func (s *Incidents) Create(tenantID, by int, in domain.NewIncident) (inc *domain.Incident, err error) {
	err = s.update(tenantID, by, domain.PermCreate, func(t *domain.Tenant) error {
		// Asignar y vincular son decisiones de quien resuelve; quien solo reporta no las toma.
		if !t.Can(by, domain.PermResolve) {
			in.AssigneeID, in.ProblemID = 0, 0
		}
		if in.Title = strings.TrimSpace(in.Title); in.Title == "" {
			return errors.New("contá qué está pasando")
		}
		if !domain.Has(t.Config.Services, in.Service) {
			return errors.New("elegí un servicio del catálogo")
		}
		if !domain.Has(domain.Priorities, in.Priority) {
			return errors.New("prioridad inválida")
		}
		if in.AssigneeID != 0 && t.User(in.AssigneeID) == nil {
			return fmt.Errorf("usuario %d: %w", in.AssigneeID, domain.ErrNotFound)
		}
		fields, err := customFields(t.Config.Fields, in.Fields)
		if err != nil {
			return err
		}
		// Se valida el problema antes de agregar el incidente: si falla, la cuenta no tiene que quedar tocada.
		if err := linkable(t, in.ProblemID); err != nil {
			return err
		}
		now := time.Now()
		inc = &domain.Incident{ID: len(t.Incidents) + 1, Title: in.Title, Description: in.Description, Service: in.Service,
			Priority: in.Priority, Status: t.Config.States[0], Fields: fields, CreatedBy: by, AssigneeID: in.AssigneeID, CreatedAt: now}
		if t.Config.Modules.SLA {
			due := now.Add(time.Duration(t.Config.SLAHours[in.Priority]) * time.Hour)
			inc.DueAt = &due
		}
		t.Incidents = append(t.Incidents, inc)
		link(t, inc, in.ProblemID, by)
		return nil
	})
	return inc, err
}

// customFields valida los campos propios de la cuenta y descarta los que no existen.
func customFields(defs []domain.Field, in map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for _, f := range defs {
		v := strings.TrimSpace(in[f.Key])
		switch {
		case v == "" && f.Required:
			return nil, fmt.Errorf("falta completar: %s", f.Label)
		case v == "":
			continue
		case f.Type == "number":
			if _, err := strconv.ParseFloat(v, 64); err != nil {
				return nil, fmt.Errorf("%s tiene que ser un número", f.Label)
			}
		case f.Type == "select" && !domain.Has(f.Options, v):
			return nil, fmt.Errorf("opción inválida en %s", f.Label)
		}
		out[f.Key] = v
	}
	return out, nil
}

// Update cambia el estado, el asignado o el problema de un incidente.
// Valida todo el pedido antes de aplicar cualquier cambio.
func (s *Incidents) Update(tenantID, by, id int, in domain.IncidentPatch) error {
	return s.update(tenantID, by, domain.PermResolve, func(t *domain.Tenant) error {
		inc, err := t.Incident(id)
		if err != nil {
			return err
		}
		if in.AssigneeID != nil && *in.AssigneeID != 0 && t.User(*in.AssigneeID) == nil {
			return fmt.Errorf("usuario %d: %w", *in.AssigneeID, domain.ErrNotFound)
		}
		if in.ProblemID != nil {
			if err := linkable(t, *in.ProblemID); err != nil {
				return err
			}
		}
		if in.Status != nil && !domain.Has(t.Config.States, *in.Status) {
			return errors.New("estado inválido")
		}

		if in.AssigneeID != nil {
			inc.AssigneeID = *in.AssigneeID
		}
		if in.ProblemID != nil {
			link(t, inc, *in.ProblemID, by)
		}
		if in.Status != nil {
			setStatus(t, inc, *in.Status, by)
		}
		return nil
	})
}

// linkable verifica que se pueda vincular un incidente al problema. 0 significa "sin problema" y siempre se puede.
func linkable(t *domain.Tenant, problemID int) error {
	if problemID == 0 {
		return nil
	}
	if !t.Config.Modules.Problems {
		return errors.New("la gestión de problemas no está activada en esta cuenta")
	}
	p, err := t.Problem(problemID)
	if err != nil {
		return err
	}
	if p.Status == domain.Resuelto {
		return errors.New("el problema ya está resuelto")
	}
	return nil
}

// link vincula (o con 0, desvincula) el incidente y lo deja en el historial. Requiere linkable.
func link(t *domain.Tenant, inc *domain.Incident, problemID, by int) {
	if inc.ProblemID == problemID {
		return
	}
	if old, err := t.Problem(inc.ProblemID); err == nil {
		old.Log(by, "", "#%d desvinculado del problema", inc.ID)
	}
	inc.ProblemID = problemID
	if p, err := t.Problem(problemID); err == nil {
		p.Log(by, "", "#%d vinculado al problema", inc.ID)
	}
}

// setStatus cambia el estado del incidente. Requiere que el estado exista en la configuración.
func setStatus(t *domain.Tenant, inc *domain.Incident, status string, by int) {
	wasOpen := inc.ResolvedAt == nil
	inc.Status = status
	if status != t.ClosedState() {
		inc.ResolvedAt = nil
		return
	}
	if wasOpen {
		now := time.Now()
		inc.ResolvedAt = &now
		if p, err := t.Problem(inc.ProblemID); err == nil && p.Workaround != "" && p.Status != domain.Resuelto {
			p.Log(by, "", "#%d resuelto con workaround", inc.ID)
		}
	}
}
