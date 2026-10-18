package repository

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"taas-backend/internal/domain"
)

// JSONTenants guarda todas las cuentas en un archivo que se reescribe entero en cada cambio.
//
// ponytail: alcanza para una demo. Las cuentas viven en memoria y Get devuelve el puntero
// compartido, así que la exclusión mutua la pone el servicio (ver service.Services).
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
	if err := r.load(); err != nil {
		return nil, err
	}
	if err := r.autoClose(); err != nil {
		return nil, fmt.Errorf("guardar el cierre automático: %w", err)
	}
	return r, nil
}

func (r *JSONTenants) load() error {
	var tenants []*domain.Tenant
	data, err := os.ReadFile(r.path)
	if err == nil {
		err = json.Unmarshal(data, &tenants)
	} else if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if err != nil {
		return err
	}
	for _, t := range tenants {
		t.Normalize() // datos guardados por una versión anterior
	}
	r.tenants = tenants
	return nil
}

// autoClose hace el barrido de cierre automático de todas las cuentas recién cargadas y lo guarda si cerró algo
// (después, cada pedido barre su cuenta: ver service.base.modify).
func (r *JSONTenants) autoClose() error {
	n := 0
	for _, t := range r.tenants {
		n += t.AutoClose(time.Now())
	}
	if n == 0 {
		return nil
	}
	return r.flush()
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
func (r *JSONTenants) Save(*domain.Tenant) error { return r.flush() }

// flush: si falla la escritura toma lo utlimo en mem, evitamos block
func (r *JSONTenants) flush() error {
	if r.path == "" {
		return nil
	}
	if err := r.write(); err != nil {
		if lerr := r.load(); lerr != nil {
			return errors.Join(err, fmt.Errorf("volver a lo guardado: %w", lerr))
		}
		return err
	}
	return nil
}

func (r *JSONTenants) write() error {
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
