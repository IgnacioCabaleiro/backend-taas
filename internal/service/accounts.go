package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

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
	name, email, err := credentials(name, email, password)
	if err != nil {
		return err
	}
	hash := HashPassword(password) // fuera del lock: tarda a propósito
	return a.update(tenantID, by, domain.PermConfig, func(t *domain.Tenant) error {
		if len(t.Users)-1 >= domain.MaxUsers {
			return fmt.Errorf("tu plan incluye hasta %d usuarios además del titular", domain.MaxUsers)
		}
		if t.Role(roleID) == nil {
			return errors.New("elegí un rol")
		}
		if err := unique(a.tenants, email); err != nil {
			return err
		}
		t.Users = append(t.Users, domain.User{ID: t.NextUserID, Name: name, Email: email, RoleID: roleID, Hash: hash})
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
		r := t.Role(roleID)
		if r == nil {
			return errors.New("elegí un rol")
		}
		// Solo se bloquea lo que el cambio le saca: el rol anterior tenía el permiso y el nuevo no.
		old := t.Role(u.RoleID)
		if err := keepsDuties(t, []int{userID}, loses(old, r.Perms, domain.PermResolve), loses(old, r.Perms, domain.PermProblems)); err != nil {
			return err
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
			now := time.Now()
			for _, inc := range t.Incidents {
				if inc.AssigneeID == userID && inc.ResolvedAt == nil {
					inc.AssigneeID = 0 // sus tickets abiertos vuelven a la cola
					inc.Log(now, by, domain.EvAssigned, "Sin asignar (se eliminó a %s)", u.Name)
				}
			}

			for _, p := range t.Problems {
				if p.OwnerID == userID {
					p.OwnerID = t.OwnerID
					p.Log(by, "", "Responsable: %s (se eliminó a %s)", t.User(t.OwnerID).Name, u.Name)
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
		ids := []int{}
		for _, u := range t.Users {
			if u.RoleID == roleID {
				ids = append(ids, u.ID)
			}
		}
		if err := keepsDuties(t, ids, loses(r, clean, domain.PermResolve), loses(r, clean, domain.PermProblems)); err != nil {
			return err
		}
		r.Name, r.Perms = name, clean
		return nil
	})
}

// loses indica si pasar del rol old a los permisos perms le saca el permiso perm a quien lo tenía.
func loses(old *domain.Role, perms []string, perm string) bool {
	return old != nil && domain.Has(old.Perms, perm) && !domain.Has(perms, perm)
}

// keepsDuties verifica que a esos usuarios se les puedan sacar los permisos que pierden: sin resolver no pueden
// tener incidentes abiertos asignados (ver assignable), y sin problemas no pueden ser responsables de un problema
// activo (ver Problems.SetOwner). Si pierden los dos, el error lista las dos cosas: qué hay que reasignar primero.
func keepsDuties(t *domain.Tenant, userIDs []int, resolve, problems bool) error {
	incidents, owned := []string{}, []string{}
	for _, id := range userIDs {
		name := t.User(id).Name
		open, active := []string{}, []string{}
		for _, inc := range t.Incidents {
			if resolve && inc.AssigneeID == id && inc.ResolvedAt == nil {
				open = append(open, fmt.Sprintf("#%d", inc.ID))
			}
		}
		for _, p := range t.Problems {
			if problems && p.OwnerID == id && p.Status != domain.Resuelto {
				active = append(active, fmt.Sprintf("P-%d", p.ID))
			}
		}
		if len(open) > 0 {
			incidents = append(incidents, fmt.Sprintf("%s (%s)", name, strings.Join(open, ", ")))
		}
		if len(active) > 0 {
			owned = append(owned, fmt.Sprintf("%s de %s", joinAnd(active), name))
		}
	}
	msgs := []string{}
	if len(incidents) > 0 {
		msgs = append(msgs, "sin el permiso resolver no se pueden tener incidentes abiertos asignados: reasigná primero los de "+
			strings.Join(incidents, " y "))
	}
	if len(owned) > 0 {
		msgs = append(msgs, "sin el permiso problemas no se puede ser responsable de un problema activo: pasá primero "+
			strings.Join(owned, " y ")+" a otro responsable")
	}
	if len(msgs) > 0 {
		return errors.New(strings.Join(msgs, "; "))
	}
	return nil
}

// joinAnd: "a", "a y b", "a, b y c".
func joinAnd(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " y " + items[len(items)-1]
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
