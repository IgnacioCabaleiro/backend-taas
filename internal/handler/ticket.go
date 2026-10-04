package handler

import (
	"net/http"

	"taas-backend/internal/domain"
)

// Incidentes y problemas.

func (h *Handler) createIncident(r *http.Request, tenantID, userID int) error {
	var in domain.NewIncident
	if err := decode(r, &in); err != nil {
		return err
	}
	_, err := h.svc.Incidents.Create(tenantID, userID, in)
	return err
}

func (h *Handler) updateIncident(r *http.Request, tenantID, userID int) error {
	var in domain.IncidentPatch
	if err := decode(r, &in); err != nil {
		return err
	}
	return h.svc.Incidents.Update(tenantID, userID, pathID(r), in)
}

func (h *Handler) createProblem(r *http.Request, tenantID, userID int) error {
	var in domain.NewProblem
	if err := decode(r, &in); err != nil {
		return err
	}
	_, err := h.svc.Problems.Create(tenantID, userID, in)
	return err
}

func (h *Handler) advanceProblem(r *http.Request, tenantID, userID int) error {
	var in domain.Advance
	if err := decode(r, &in); err != nil {
		return err
	}
	return h.svc.Problems.Advance(tenantID, userID, pathID(r), in)
}

func (h *Handler) setProblemOwner(r *http.Request, tenantID, userID int) error {
	var in struct {
		OwnerID int `json:"ownerId"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	return h.svc.Problems.SetOwner(tenantID, userID, pathID(r), in.OwnerID)
}

func (h *Handler) commentProblem(r *http.Request, tenantID, userID int) error {
	var in struct{ Text string }
	if err := decode(r, &in); err != nil {
		return err
	}
	return h.svc.Problems.Comment(tenantID, userID, pathID(r), in.Text)
}
