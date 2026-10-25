package handler

import (
	"net/http"
	"strconv"

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

func (h *Handler) commentIncident(r *http.Request, tenantID, userID int) error {
	var in struct {
		Text   string
		Public bool
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	return h.svc.Incidents.Comment(tenantID, userID, pathID(r), in.Text, in.Public)
}

func (h *Handler) reopenIncident(r *http.Request, tenantID, userID int) error {
	var in struct{ Reason string }
	if err := decode(r, &in); err != nil {
		return err
	}
	return h.svc.Incidents.Reopen(tenantID, userID, pathID(r), in.Reason)
}

func (h *Handler) closeIncident(r *http.Request, tenantID, userID int) error {
	return h.svc.Incidents.Close(tenantID, userID, pathID(r))
}

// Métricas de SLA: responde con los indicadores (no con el estado de la cuenta, como el resto).
func (h *Handler) slaMetrics(w http.ResponseWriter, r *http.Request) {
	tenantID, userID, err := h.svc.Auth.Authenticate(bearer(r))
	if err != nil {
		fail(w, err)
		return
	}
	days := 30
	if q := r.URL.Query().Get("days"); q != "" {
		if days, err = strconv.Atoi(q); err != nil {
			days = 0 // el servicio lo rechaza con el mensaje de los períodos válidos
		}
	}
	m, err := h.svc.Metrics.SLA(tenantID, userID, days)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
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
