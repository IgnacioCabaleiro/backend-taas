package handler

import "net/http"

func (h *Handler) signup(w http.ResponseWriter, r *http.Request) {
	var in struct{ Company, Name, Email, Password string }
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	token, err := h.svc.Auth.Signup(in.Company, in.Name, in.Email, in.Password)
	respondToken(w, token, err)
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Password string }
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	token, err := h.svc.Auth.Login(in.Email, in.Password)
	respondToken(w, token, err)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	h.svc.Auth.Logout(bearer(r))
	w.WriteHeader(http.StatusNoContent)
}

func respondToken(w http.ResponseWriter, token string, err error) {
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}
