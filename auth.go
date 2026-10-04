package main

// Cuentas, usuarios, roles y sesiones.

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strings"
)

// Permisos: lo que el titular decide que cada rol puede ver y hacer.
const (
	PermPanel    = "panel"     // ver el panel
	PermCreate   = "crear"     // crear tickets
	PermResolve  = "resolver"  // atender y resolver tickets
	PermProblems = "problemas" // gestionar problemas y errores conocidos
	PermConfig   = "config"    // configurar la cuenta, sus usuarios y roles
)

var allPerms = []string{PermPanel, PermCreate, PermResolve, PermProblems, PermConfig}

// maxUsers: usuarios que el titular puede crear además del suyo.
const maxUsers = 5

type Role struct {
	ID     int      `json:"id"`
	Name   string   `json:"name"`
	Perms  []string `json:"perms"`
	Locked bool     `json:"locked"` // el rol del titular: no se edita ni se borra
}

type User struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	RoleID int    `json:"roleId"`
	Hash   string `json:"hash,omitempty"` // nunca sale por la API (ver Tenant.view)
}

func defaultRoles() []Role {
	return []Role{
		{1, "Administrador", allPerms, true},
		{2, "Agente", []string{PermPanel, PermCreate, PermResolve, PermProblems}, false},
		{3, "Solicitante", []string{PermCreate}, false},
	}
}

func (t *Tenant) user(id int) *User {
	for i := range t.Users {
		if t.Users[i].ID == id {
			return &t.Users[i]
		}
	}
	return nil
}

func (t *Tenant) role(id int) *Role {
	for i := range t.Roles {
		if t.Roles[i].ID == id {
			return &t.Roles[i]
		}
	}
	return nil
}

func (t *Tenant) can(by int, perm string) bool {
	u := t.user(by)
	if u == nil {
		return false
	}
	r := t.role(u.RoleID)
	return r != nil && has(r.Perms, perm)
}

func (t *Tenant) need(by int, perm string) error {
	if !t.can(by, perm) {
		return errForbidden
	}
	return nil
}

// --- Contraseñas ---

const pbkdf2Iter = 600_000 // recomendación OWASP para PBKDF2-HMAC-SHA256

func hashPassword(password string) string {
	salt := make([]byte, 16)
	rand.Read(salt)
	key, _ := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iter, 32)
	return hex.EncodeToString(salt) + ":" + hex.EncodeToString(key)
}

func checkPassword(hash, password string) bool {
	saltHex, keyHex, ok := strings.Cut(hash, ":")
	salt, err1 := hex.DecodeString(saltHex)
	want, err2 := hex.DecodeString(keyHex)
	if !ok || err1 != nil || err2 != nil {
		return false
	}
	got, _ := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iter, 32)
	return subtle.ConstantTimeCompare(got, want) == 1
}

func (s *Store) findEmail(email string) (*Tenant, *User) {
	for _, t := range s.Tenants {
		for i := range t.Users {
			if t.Users[i].Email == email {
				return t, &t.Users[i]
			}
		}
	}
	return nil, nil
}

// credentials valida y normaliza los datos de un usuario nuevo. El email es único en toda la plataforma.
func (s *Store) credentials(name, email, password string) (string, string, error) {
	name, email = strings.TrimSpace(name), strings.ToLower(strings.TrimSpace(email))
	if name == "" {
		return "", "", errors.New("falta el nombre")
	}
	if a, err := mail.ParseAddress(email); err != nil || a.Address != email {
		return "", "", errors.New("el email no es válido")
	}
	if len(password) < 8 {
		return "", "", errors.New("la contraseña tiene que tener al menos 8 caracteres")
	}
	if _, u := s.findEmail(email); u != nil {
		return "", "", errors.New("ya hay una cuenta con ese email")
	}
	return name, email, nil
}

// --- Sesiones ---

// ponytail: sesiones en memoria, sin vencimiento: se pierden al reiniciar y hay que volver a entrar.
// Tampoco hay límite de intentos de login. Agregar ambos antes de exponerlo a internet.
type session struct{ tenant, user int }

func (s *Store) newSession(t *Tenant, u *User) string {
	b := make([]byte, 32)
	rand.Read(b)
	tok := hex.EncodeToString(b)
	s.sessions[tok] = session{t.ID, u.ID}
	return tok
}

func bearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func (s *Store) auth(r *http.Request) (*Tenant, int, error) {
	ses, ok := s.sessions[bearer(r)]
	if !ok || ses.tenant < 1 || ses.tenant > len(s.Tenants) {
		return nil, 0, errUnauthorized
	}
	t := s.Tenants[ses.tenant-1]
	if t.user(ses.user) == nil {
		return nil, 0, errUnauthorized
	}
	return t, ses.user, nil
}

// Signup es la contratación: crea la cuenta del cliente con su titular. La configuración
// definitiva se elige después, en el onboarding; mientras tanto queda una mínima válida.
func (s *Store) Signup(company, name, email, password string) (string, error) {
	if company = strings.TrimSpace(company); company == "" {
		return "", errors.New("falta el nombre de la empresa")
	}
	name, email, err := s.credentials(name, email, password)
	if err != nil {
		return "", err
	}
	c, _ := templates["otro"].build(Setup{})
	t := &Tenant{ID: len(s.Tenants) + 1, Name: company, Config: c, Roles: defaultRoles(), OwnerID: 1,
		Users: []User{{1, name, email, 1, hashPassword(password)}}, NextUserID: 2, NextRoleID: 4,
		Incidents: []*Incident{}, Problems: []*Problem{}}
	s.Tenants = append(s.Tenants, t)
	return s.newSession(t, &t.Users[0]), nil
}

func (s *Store) Login(email, password string) (string, error) {
	t, u := s.findEmail(strings.ToLower(strings.TrimSpace(email)))
	if u == nil || !checkPassword(u.Hash, password) {
		return "", errors.New("email o contraseña incorrectos")
	}
	return s.newSession(t, u), nil
}

// --- Usuarios de la cuenta ---

func (s *Store) AddUser(t *Tenant, name, email, password string, roleID, by int) error {
	if err := t.need(by, PermConfig); err != nil {
		return err
	}
	if len(t.Users)-1 >= maxUsers {
		return fmt.Errorf("tu plan incluye hasta %d usuarios además del titular", maxUsers)
	}
	if t.role(roleID) == nil {
		return errors.New("elegí un rol")
	}
	name, email, err := s.credentials(name, email, password)
	if err != nil {
		return err
	}
	t.Users = append(t.Users, User{t.NextUserID, name, email, roleID, hashPassword(password)})
	t.NextUserID++
	return nil
}

func (t *Tenant) SetUserRole(id, roleID, by int) error {
	if err := t.need(by, PermConfig); err != nil {
		return err
	}
	u := t.user(id)
	if u == nil {
		return fmt.Errorf("usuario %d: %w", id, errNotFound)
	}
	if id == t.OwnerID {
		return errors.New("el titular de la cuenta siempre es administrador")
	}
	if t.role(roleID) == nil {
		return errors.New("elegí un rol")
	}
	u.RoleID = roleID
	return nil
}

func (s *Store) DeleteUser(t *Tenant, id, by int) error {
	if err := t.need(by, PermConfig); err != nil {
		return err
	}
	if id == t.OwnerID || id == by {
		return errors.New("no se puede eliminar al titular ni al usuario con el que estás operando")
	}
	for i, u := range t.Users {
		if u.ID != id {
			continue
		}
		t.Users = append(t.Users[:i], t.Users[i+1:]...)
		for _, inc := range t.Incidents {
			if inc.AssigneeID == id && inc.ResolvedAt == nil {
				inc.AssigneeID = 0 // sus tickets abiertos vuelven a la cola
			}
		}
		for tok, ses := range s.sessions {
			if ses.tenant == t.ID && ses.user == id {
				delete(s.sessions, tok)
			}
		}
		return nil
	}
	return fmt.Errorf("usuario %d: %w", id, errNotFound)
}

// --- Roles ---

// SaveRole crea (id 0) o edita un rol.
func (t *Tenant) SaveRole(id int, name string, perms []string, by int) error {
	if err := t.need(by, PermConfig); err != nil {
		return err
	}
	if name = strings.TrimSpace(name); name == "" {
		return errors.New("ponele un nombre al rol")
	}
	clean := []string{}
	for _, p := range allPerms { // orden fijo, sin repetidos ni permisos inventados
		if has(perms, p) {
			clean = append(clean, p)
		}
	}
	if len(clean) == 0 {
		return errors.New("el rol necesita al menos un permiso")
	}
	for _, r := range t.Roles {
		if r.ID != id && strings.EqualFold(r.Name, name) {
			return errors.New("ya hay un rol con ese nombre")
		}
	}
	if id == 0 {
		t.Roles = append(t.Roles, Role{t.NextRoleID, name, clean, false})
		t.NextRoleID++
		return nil
	}
	r := t.role(id)
	if r == nil {
		return fmt.Errorf("rol %d: %w", id, errNotFound)
	}
	if r.Locked {
		return errors.New("el rol del titular no se puede modificar")
	}
	r.Name, r.Perms = name, clean
	return nil
}

func (t *Tenant) DeleteRole(id, by int) error {
	if err := t.need(by, PermConfig); err != nil {
		return err
	}
	for i, r := range t.Roles {
		if r.ID != id {
			continue
		}
		if r.Locked {
			return errors.New("el rol del titular no se puede eliminar")
		}
		for _, u := range t.Users {
			if u.RoleID == id {
				return fmt.Errorf("hay usuarios con el rol %s: cambiales el rol antes de eliminarlo", r.Name)
			}
		}
		t.Roles = append(t.Roles[:i], t.Roles[i+1:]...)
		return nil
	}
	return fmt.Errorf("rol %d: %w", id, errNotFound)
}
