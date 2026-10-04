// Package service tiene los casos de uso de TaaS: valida, aplica las reglas de negocio
// y persiste a través de los repositorios. No sabe nada de HTTP.
package service

import (
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
	b := base{tenants}
	return &Services{
		Auth:      &Auth{tenants, sessions},
		Accounts:  &Accounts{b, sessions},
		Setup:     &Setup{b},
		Incidents: &Incidents{b},
		Problems:  &Problems{b},
		State:     &State{tenants},
	}
}

type base struct{ tenants repository.Tenants }

// update es el esqueleto de todo caso de uso que modifica una cuenta:
// la carga, verifica que el usuario tenga el permiso, aplica fn y la guarda.
func (b base) update(tenantID, by int, perm string, fn func(t *domain.Tenant) error) error {
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
	return b.tenants.Save(t)
}
