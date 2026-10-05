// Package handler es la capa HTTP: decodifica el pedido, llama al servicio y arma la respuesta.
// No tiene reglas de negocio.
package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"taas-backend/internal/domain"
	"taas-backend/internal/service"
)

type Handler struct {
	svc *service.Services
}

// New arma el router con todas las rutas de la API.
func New(svc *service.Services) http.Handler {
	h := &Handler{svc: svc}
	mux := http.NewServeMux()

	// Sin sesión: contratar (crear la cuenta), entrar y las plantillas del onboarding.
	// Signup y login toman el lock adentro del servicio (ver service.Services); las plantillas no lo necesitan.
	mux.HandleFunc("POST /api/signup", h.signup)
	mux.HandleFunc("POST /api/login", h.login)
	mux.HandleFunc("POST /api/logout", h.locked(h.logout))
	mux.HandleFunc("GET /api/templates", h.templates)

	// Con sesión: todo responde con el estado de la cuenta (ver authed).
	mux.HandleFunc("GET /api/state", h.authed(h.state))
	mux.HandleFunc("POST /api/onboarding", h.authed(h.onboard))
	mux.HandleFunc("POST /api/onboarding/finish", h.authed(h.finishOnboarding))
	mux.HandleFunc("PUT /api/config", h.authed(h.setConfig))

	mux.HandleFunc("POST /api/users", h.authed(h.addUser))
	mux.HandleFunc("PUT /api/users/{id}", h.authed(h.setUserRole))
	mux.HandleFunc("DELETE /api/users/{id}", h.authed(h.deleteUser))
	mux.HandleFunc("POST /api/roles", h.authed(h.saveRole))
	mux.HandleFunc("PUT /api/roles/{id}", h.authed(h.saveRole))
	mux.HandleFunc("DELETE /api/roles/{id}", h.authed(h.deleteRole))

	mux.HandleFunc("POST /api/incidents", h.authed(h.createIncident))
	mux.HandleFunc("POST /api/incidents/{id}", h.authed(h.updateIncident))
	mux.HandleFunc("POST /api/problems", h.authed(h.createProblem))
	mux.HandleFunc("POST /api/problems/{id}/advance", h.authed(h.advanceProblem))
	mux.HandleFunc("POST /api/problems/{id}/owner", h.authed(h.setProblemOwner))
	mux.HandleFunc("POST /api/problems/{id}/comments", h.authed(h.commentProblem))

	return h.middleware(mux)
}

// middleware: CORS y límite de tamaño del body.
func (h *Handler) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ponytail: CORS abierto, es una demo local (la sesión va en un header, no en cookies). Restringir el origen antes de exponerlo.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// El body se lee entero antes de tomar el lock: un cliente lento no puede trabar al resto.
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "el pedido es demasiado grande o llegó incompleto"})
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}

// locked ejecuta el handler con el lock global tomado.
// ponytail: un lock global por request, porque el repositorio JSON comparte las cuentas en memoria.
// Con una base de datos real esto se va y la concurrencia la maneja la base.
func (h *Handler) locked(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.svc.Lock()
		defer h.svc.Unlock()
		next(w, r)
	}
}

// authedFunc es un handler que ya sabe de qué cuenta y de qué usuario viene el pedido.
type authedFunc func(r *http.Request, tenantID, userID int) error

// authed autentica, ejecuta fn y responde siempre con el estado completo de la cuenta
// tal como lo ve ese usuario: el front no tiene que volver a consultar.
func (h *Handler) authed(fn authedFunc) http.HandlerFunc {
	return h.locked(func(w http.ResponseWriter, r *http.Request) {
		tenantID, userID, err := h.svc.Auth.Authenticate(bearer(r))
		if err == nil {
			err = fn(r, tenantID, userID)
		}
		if err != nil {
			fail(w, err)
			return
		}
		view, err := h.svc.State.View(tenantID, userID)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, view)
	})
}

func bearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("escribir la respuesta: %v", err)
	}
}

// fail traduce un error de dominio a su código HTTP. Lo que no reconoce es un error de validación.
// Los errores internos se registran en el log y al cliente le llega un mensaje genérico.
func fail(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, domain.ErrInternal):
		log.Printf("error interno: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "algo falló de nuestro lado, probá de nuevo en un rato"})
		return
	case errors.Is(err, domain.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, domain.ErrForbidden):
		status = http.StatusForbidden
	case errors.Is(err, domain.ErrUnauthorized):
		status = http.StatusUnauthorized
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func decode(r *http.Request, v any) error {
	if json.NewDecoder(r.Body).Decode(v) != nil {
		return errors.New("JSON inválido")
	}
	return nil
}

// pathID lee el {id} de la ruta; si no es un número devuelve 0, que ningún recurso usa.
func pathID(r *http.Request) int {
	n, _ := strconv.Atoi(r.PathValue("id"))
	return n
}
