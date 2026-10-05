package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"taas-backend/internal/repository"
	"taas-backend/internal/service"
)

// Un cliente que manda el body de a poco no puede trabar al resto de los pedidos.
func TestSlowBodyDoesNotBlock(t *testing.T) {
	tenants, _ := repository.NewJSONTenants("")
	h := New(service.New(tenants, repository.NewMemorySessions()))

	body, w := io.Pipe()
	defer w.Close()
	go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/incidents", body))
	w.Write([]byte("{")) // y nunca termina

	done := make(chan int)
	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/state", nil))
		done <- rec.Code
	}()
	select {
	case code := <-done:
		if code != http.StatusUnauthorized {
			t.Fatalf("esperaba 401 sin sesión, obtuve %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("el pedido quedó bloqueado por otro con el body incompleto")
	}
}

// Un body más grande que el límite se rechaza con 413.
func TestBodyTooLarge(t *testing.T) {
	tenants, _ := repository.NewJSONTenants("")
	h := New(service.New(tenants, repository.NewMemorySessions()))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/login", strings.NewReader(strings.Repeat("x", 2<<20))))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("esperaba 413, obtuve %d", rec.Code)
	}
}
