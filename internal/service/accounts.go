package service

import (
	"errors"
	"fmt"
	"strings"

	"taas-backend/internal/domain"
	"taas-backend/internal/repository"
)

// Accounts: los usuarios y roles de una cuenta. Todo requiere el permiso de configurar.
type Accounts struct {
	base
	sessions repository.Sessions
}

// DefaultRoles: los roles con los que arranca toda cuenta. El titular puede sumar los suyos.
func DefaultRoles() []domain.Role {
	return []domain.Role{
		{ID: 1, Name: "Administrador", Perms: domain.AllPerms, Locked: true},
		{ID: 2, Name: "Agente", Perms: []string{domain.PermPanel, domain.PermCreate, domain.PermResolve, domain.PermProblems}},
		{ID: 3, Name: "Solicitante", Perms: []string{domain.PermCreate}},
	}
}

func (a *Accounts) AddUser(tenantID, by int, name, email, password string, roleID int) error {
	return a.update(tenantID, by, domain.PermConfig, func(t *domain.Tenant) error {
		if len(t.Users)-1 >= domain.MaxUsers {
			return fmt.Errorf("tu plan incluye hasta %d usuarios además del titular", domain.MaxUsers)
		}
		if t.Role(roleID) == nil {
			return errors.New("elegí un rol")
		}
		name, email, err := credentials(a.tenants, name, email, password)
		if err != nil {
			return err
		}
		t.Users = append(t.Users, domain.User{ID: t.NextUserID, Name: name, Email: email, RoleID: roleID, Hash: HashPassword(password)})
		t.NextUserID++
		return nil
	})
}

func (a *Accounts) SetUserRole(tenantID, by, userID, roleID int) error {
	return a.update(tenantID, by, domain.PermConfig, func(t *domain.Tenant) error {
		u := t.User(userID)
		if u == nil {
			return fmt.Errorf("usuario %d: %w", userID, domain.ErrNotFound)
		}
		if userID == t.OwnerID {
			return errors.New("el titular de la cuenta siempre es administrador")
		}
		if t.Role(roleID) == nil {
			return errors.New("elegí un rol")
		}
		u.RoleID = roleID
		return nil
	})
}

func (a *Accounts) DeleteUser(tenantID, by, userID int) error {
	err := a.update(tenantID, by, domain.PermConfig, func(t *domain.Tenant) error {
		if userID == t.OwnerID || userID == by {
			return errors.New("no se puede eliminar al titular ni al usuario con el que estás operando")
		}
		for i, u := range t.Users {
			if u.ID != userID {
				continue
			}
			t.Users = append(t.Users[:i], t.Users[i+1:]...)
			for _, inc := range t.Incidents {
				if inc.AssigneeID == userID && inc.ResolvedAt == nil {
					inc.AssigneeID = 0 // sus tickets abiertos vuelven a la cola
				}
			}
			return nil
		}
		return fmt.Errorf("usuario %d: %w", userID, domain.ErrNotFound)
	})
	if err == nil {
		a.sessions.DeleteUser(tenantID, userID)
	}
	return err
}

// SaveRole crea (roleID 0) o edita un rol.
func (a *Accounts) SaveRole(tenantID, by, roleID int, name string, perms []string) error {
	return a.update(tenantID, by, domain.PermConfig, func(t *domain.Tenant) error {
		if name = strings.TrimSpace(name); name == "" {
			return errors.New("ponele un nombre al rol")
		}
		clean := []string{}
		for _, p := range domain.AllPerms { // orden fijo, sin repetidos ni permisos inventados
			if domain.Has(perms, p) {
				clean = append(clean, p)
			}
		}
		if len(clean) == 0 {
			return errors.New("el rol necesita al menos un permiso")
		}
		for _, r := range t.Roles {
			if r.ID != roleID && strings.EqualFold(r.Name, name) {
				return errors.New("ya hay un rol con ese nombre")
			}
		}
		if roleID == 0 {
			t.Roles = append(t.Roles, domain.Role{ID: t.NextRoleID, Name: name, Perms: clean})
			t.NextRoleID++
			return nil
		}
		r := t.Role(roleID)
		if r == nil {
			return fmt.Errorf("rol %d: %w", roleID, domain.ErrNotFound)
		}
		if r.Locked {
			return errors.New("el rol del titular no se puede modificar")
		}
		r.Name, r.Perms = name, clean
		return nil
	})
}

func (a *Accounts) DeleteRole(tenantID, by, roleID int) error {
	return a.update(tenantID, by, domain.PermConfig, func(t *domain.Tenant) error {
		for i, r := range t.Roles {
			if r.ID != roleID {
				continue
			}
			if r.Locked {
				return errors.New("el rol del titular no se puede eliminar")
			}
			for _, u := range t.Users {
				if u.RoleID == roleID {
					return fmt.Errorf("hay usuarios con el rol %s: cambiales el rol antes de eliminarlo", r.Name)
				}
			}
			t.Roles = append(t.Roles[:i], t.Roles[i+1:]...)
			return nil
		}
		return fmt.Errorf("rol %d: %w", roleID, domain.ErrNotFound)
	})
}
