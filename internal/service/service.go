// Package service tiene los casos de uso de TaaS: valida, aplica las reglas de negocio
// y persiste a través de los repositorios. No sabe nada de HTTP.
package service

import (
	"fmt"
	"sync"

	"taas-backend/internal/domain"
	"taas-backend/internal/repository"
)

type Services struct {
	Auth      *Auth
	Accounts  *Accounts
	Setup     *Setup
	Incidents *Incidents
	Problems  *Problems
	State     *State
}

func New(tenants repository.Tenants, sessions repository.Sessions) *Services {
	b := base{tenants, &sync.Mutex{}}
	return &Services{
		Auth:      &Auth{b, sessions},
		Accounts:  &Accounts{b, sessions},
		Setup:     &Setup{b},
		Incidents: &Incidents{b},
		Problems:  &Problems{b},
		State:     &State{b},
	}
}

type base struct {
	tenants repository.Tenants
	mu      *sync.Mutex
}

// update es el esqueleto de todo caso de uso que modifica una cuenta:
// con el lock tomado, la carga, verifica que el usuario tenga el permiso, aplica fn y la guarda.
// fn trabaja sobre la cuenta en memoria: tiene que validar todo antes de modificarla,
// porque si devuelve un error a mitad de camino lo ya cambiado no se deshace.
func (b base) update(tenantID, by int, perm string, fn func(t *domain.Tenant) error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, err := b.tenants.Get(tenantID)
	if err != nil {
		return err
	}
	if !t.Can(by, perm) {
		return domain.ErrForbidden
	}
	if err := fn(t); err != nil {
		return err
	}
	if err := b.tenants.Save(t); err != nil {
		return fmt.Errorf("guardar la cuenta %d: %v: %w", t.ID, err, domain.ErrInternal)
	}
	return nil
}
