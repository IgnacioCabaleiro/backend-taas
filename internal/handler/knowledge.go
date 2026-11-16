package handler

import (
	"net/http"
	"strconv"

	"taas-backend/internal/domain"
)

// Errores conocidos, workarounds e incidentes parecidos.

// respondRead autentica, ejecuta una consulta y responde con lo que devuelve (no con el estado de la cuenta).
func (h *Handler) respondRead(w http.ResponseWriter, r *http.Request, fn func(tenantID, userID int) (any, error)) {
	tenantID, userID, err := h.svc.Auth.Authenticate(bearer(r))
	var out any
	if err == nil {
		out, err = fn(tenantID, userID)
	}
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) matchKnowledge(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	h.respondRead(w, r, func(tenantID, userID int) (any, error) {
		return h.svc.Knowledge.Match(tenantID, userID, q.Get("text"), q.Get("service"))
	})
}

func (h *Handler) relatedToIncident(w http.ResponseWriter, r *http.Request) {
	h.respondRead(w, r, func(tenantID, userID int) (any, error) {
		return h.svc.Knowledge.Related(tenantID, userID, pathID(r))
	})
}

func (h *Handler) applyWorkaround(r *http.Request, tenantID, userID int) error {
	var in struct {
		ProblemID int `json:"problemId"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	return h.svc.Incidents.ApplyWorkaround(tenantID, userID, pathID(r), in.ProblemID)
}

func (h *Handler) updateWorkaround(r *http.Request, tenantID, userID int) error {
	var in struct {
		Text   string `json:"text"`
		Shared bool   `json:"shared"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	return h.svc.Problems.UpdateWorkaround(tenantID, userID, pathID(r), in.Text, in.Shared)
}

func (h *Handler) saveReview(r *http.Request, tenantID, userID int) error {
	var in domain.Review
	if err := decode(r, &in); err != nil {
		return err
	}
	return h.svc.Problems.SaveReview(tenantID, userID, pathID(r), in)
}

func (h *Handler) setActionDone(r *http.Request, tenantID, userID int) error {
	var in struct {
		Done bool `json:"done"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	index, _ := strconv.Atoi(r.PathValue("n"))
	return h.svc.Problems.SetActionDone(tenantID, userID, pathID(r), index, in.Done)
}

func (h *Handler) deflect(r *http.Request, tenantID, userID int) error {
	return h.svc.Problems.Deflect(tenantID, userID, pathID(r))
}
