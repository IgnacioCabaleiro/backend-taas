// Package service tiene los casos de uso de TaaS: valida, aplica las reglas de negocio
// y persiste a través de los repositorios. No sabe nada de HTTP.
package service

import (
	"fmt"
	"sync"

	"taas-backend/internal/domain"
	"taas-backend/internal/repository"
)

// Services agrupa los casos de uso. Su mutex protege las cuentas y sesiones en memoria:
// la capa HTTP lo toma en cada pedido (Lock/Unlock), salvo login y alta, que lo manejan
// adentro para no derivar contraseñas (lento a propósito) con todo el servidor bloqueado.
type Services struct {
	sync.Mutex
	Auth      *Auth
	Accounts  *Accounts
	Setup     *Setup
	Incidents *Incidents
	Problems  *Problems
	State     *State
}

func New(tenants repository.Tenants, sessions repository.Sessions) *Services {
	b := base{tenants}
	s := &Services{
		Accounts:  &Accounts{b, sessions},
		Setup:     &Setup{b},
		Incidents: &Incidents{b},
		Problems:  &Problems{b},
		State:     &State{tenants},
	}
	s.Auth = &Auth{tenants, sessions, &s.Mutex}
	return s
}

type base struct{ tenants repository.Tenants }

// update es el esqueleto de todo caso de uso que modifica una cuenta:
// la carga, verifica que el usuario tenga el permiso, aplica fn y la guarda.
// fn trabaja sobre la cuenta en memoria: tiene que validar todo antes de modificarla,
// porque si devuelve un error a mitad de camino lo ya cambiado no se deshace.
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
	if err := b.tenants.Save(t); err != nil {
		return fmt.Errorf("guardar la cuenta %d: %v: %w", t.ID, err, domain.ErrInternal)
	}
	return nil
}
