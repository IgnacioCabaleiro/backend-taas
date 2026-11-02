// Package service tiene los casos de uso de TaaS: valida, aplica las reglas de negocio
// y persiste a través de los repositorios. No sabe nada de HTTP.
package service

import (
	"fmt"
	"sync"
	"time"

	"taas-backend/internal/domain"
	"taas-backend/internal/repository"
)

type Services struct {
	Auth        *Auth
	Accounts    *Accounts
	Setup       *Setup
	Incidents   *Incidents
	Problems    *Problems
	Attachments *Attachments
	Metrics     *Metrics
	State       *State
}

func New(tenants repository.Tenants, sessions repository.Sessions, blobs repository.Blobs) *Services {
	b := base{tenants, &sync.Mutex{}}
	return &Services{
		Auth:        &Auth{b, sessions},
		Accounts:    &Accounts{b, sessions},
		Setup:       &Setup{b},
		Incidents:   &Incidents{b},
		Problems:    &Problems{b},
		Attachments: &Attachments{b, blobs},
		Metrics:     &Metrics{b},
		State:       &State{b},
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
	return b.modify(tenantID, func(t *domain.Tenant) error {
		if !t.Can(by, perm) {
			return domain.ErrForbidden
		}
		return fn(t)
	})
}

// modify es update sin el chequeo de permiso, para los casos de uso en los que quién puede hacerlo
// depende del incidente (por ejemplo, quien lo reportó). fn verifica los permisos antes de modificar nada.
// Antes de fn hace el barrido de cierre automático, para que fn vea las etapas al día.
func (b base) modify(tenantID int, fn func(t *domain.Tenant) error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, err := b.tenants.Get(tenantID)
	if err != nil {
		return err
	}
	closed := t.AutoClose(time.Now())
	if err := fn(t); err != nil {
		// fn no tocó nada (valida antes de modificar), pero el barrido sí: se guarda igual. Si no se puede,
		// el repositorio vuelve a lo guardado y el próximo pedido barre de nuevo, así que alcanza con el error de fn.
		if closed > 0 {
			b.tenants.Save(t)
		}
		return err
	}
	if err := b.tenants.Save(t); err != nil {
		return fmt.Errorf("guardar la cuenta %d: %v: %w", t.ID, err, domain.ErrInternal)
	}
	return nil
}
