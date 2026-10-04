package repository

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"taas-backend/internal/domain"
)

// JSONTenants guarda todas las cuentas en un archivo que se reescribe entero en cada cambio.
//
// ponytail: alcanza para una demo. Las cuentas viven en memoria y Get devuelve el puntero
// compartido, así que la exclusión mutua la pone el handler (un lock por request).
// Pasar a MySQL/Postgres con tenant_id cuando haya datos reales: solo cambia esta implementación.
type JSONTenants struct {
	path    string // "" = solo en memoria (tests)
	tenants []*domain.Tenant
}

// NewJSONTenants carga el archivo si existe; si no, arranca vacío.
func NewJSONTenants(path string) (*JSONTenants, error) {
	r := &JSONTenants{path: path}
	if path == "" {
		return r, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	return r, json.Unmarshal(data, &r.tenants)
}

func (r *JSONTenants) Empty() bool { return len(r.tenants) == 0 }

func (r *JSONTenants) Get(id int) (*domain.Tenant, error) {
	if id < 1 || id > len(r.tenants) {
		return nil, fmt.Errorf("cuenta %d: %w", id, domain.ErrNotFound)
	}
	return r.tenants[id-1], nil
}

func (r *JSONTenants) FindByEmail(email string) (*domain.Tenant, *domain.User) {
	for _, t := range r.tenants {
		for i := range t.Users {
			if t.Users[i].Email == email {
				return t, &t.Users[i]
			}
		}
	}
	return nil, nil
}

func (r *JSONTenants) Create(t *domain.Tenant) error {
	t.ID = len(r.tenants) + 1
	r.tenants = append(r.tenants, t)
	return r.flush()
}

// Save persiste la cuenta. Como Get devuelve el puntero en memoria, alcanza con volcar el archivo.
func (r *JSONTenants) Save(*domain.Tenant) error { return r.flush() }

func (r *JSONTenants) flush() error {
	if r.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(r.tenants, "", " ")
	if err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil { // tiene hashes de contraseñas
		return err
	}
	return os.Rename(tmp, r.path)
}
