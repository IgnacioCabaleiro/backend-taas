package handler

import (
	"net/http"

	"taas-backend/internal/domain"
)

// Estado, onboarding, configuración, usuarios y roles de la cuenta.

func (h *Handler) state(*http.Request, int, int) error { return nil }

func (h *Handler) templates(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.svc.Setup.Templates())
}

func (h *Handler) onboard(r *http.Request, tenantID, userID int) error {
	var in domain.Setup
	if err := decode(r, &in); err != nil {
		return err
	}
	return h.svc.Setup.Onboard(tenantID, userID, in)
}

func (h *Handler) finishOnboarding(_ *http.Request, tenantID, userID int) error {
	return h.svc.Setup.Finish(tenantID, userID)
}

func (h *Handler) setConfig(r *http.Request, tenantID, userID int) error {
	var in domain.Config
	if err := decode(r, &in); err != nil {
		return err
	}
	return h.svc.Setup.SetConfig(tenantID, userID, in)
}

func (h *Handler) addUser(r *http.Request, tenantID, userID int) error {
	var in struct {
		Name, Email, Password string
		RoleID                int `json:"roleId"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	return h.svc.Accounts.AddUser(tenantID, userID, in.Name, in.Email, in.Password, in.RoleID)
}

func (h *Handler) setUserRole(r *http.Request, tenantID, userID int) error {
	var in struct {
		RoleID int `json:"roleId"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	return h.svc.Accounts.SetUserRole(tenantID, userID, pathID(r), in.RoleID)
}

func (h *Handler) deleteUser(r *http.Request, tenantID, userID int) error {
	return h.svc.Accounts.DeleteUser(tenantID, userID, pathID(r))
}

// saveRole atiende tanto el alta (sin {id}) como la edición.
func (h *Handler) saveRole(r *http.Request, tenantID, userID int) error {
	var in struct {
		Name  string
		Perms []string
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	return h.svc.Accounts.SaveRole(tenantID, userID, pathID(r), in.Name, in.Perms)
}

func (h *Handler) deleteRole(r *http.Request, tenantID, userID int) error {
	return h.svc.Accounts.DeleteRole(tenantID, userID, pathID(r))
}
