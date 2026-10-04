package service

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/mail"
	"strings"

	"taas-backend/internal/domain"
	"taas-backend/internal/repository"
)

// Auth: contratación (alta de la cuenta), ingreso y sesiones.
type Auth struct {
	tenants  repository.Tenants
	sessions repository.Sessions
}

// Signup es la contratación: crea la cuenta del cliente con su titular. La configuración
// definitiva sale del onboarding; mientras tanto queda una mínima válida.
func (a *Auth) Signup(company, name, email, password string) (string, error) {
	if company = strings.TrimSpace(company); company == "" {
		return "", errors.New("falta el nombre de la empresa")
	}
	name, email, err := credentials(a.tenants, name, email, password)
	if err != nil {
		return "", err
	}
	cfg, _ := templates["otro"].build(domain.Setup{})
	t := &domain.Tenant{Name: company, Config: cfg, Roles: DefaultRoles(), OwnerID: 1,
		Users: []domain.User{{ID: 1, Name: name, Email: email, RoleID: 1, Hash: HashPassword(password)}}, NextUserID: 2, NextRoleID: 4,
		Incidents: []*domain.Incident{}, Problems: []*domain.Problem{}}
	if err := a.tenants.Create(t); err != nil {
		return "", err
	}
	return a.sessions.Create(repository.Session{TenantID: t.ID, UserID: 1}), nil
}

// ponytail: sin límite de intentos. Agregar rate limiting antes de exponerlo a internet.
func (a *Auth) Login(email, password string) (string, error) {
	t, u := a.tenants.FindByEmail(strings.ToLower(strings.TrimSpace(email)))
	if u == nil || !CheckPassword(u.Hash, password) {
		return "", errors.New("email o contraseña incorrectos")
	}
	return a.sessions.Create(repository.Session{TenantID: t.ID, UserID: u.ID}), nil
}

func (a *Auth) Logout(token string) { a.sessions.Delete(token) }

// Authenticate resuelve un token a la cuenta y el usuario que lo usan.
func (a *Auth) Authenticate(token string) (tenantID, userID int, err error) {
	s, ok := a.sessions.Get(token)
	if !ok {
		return 0, 0, domain.ErrUnauthorized
	}
	t, err := a.tenants.Get(s.TenantID)
	if err != nil || t.User(s.UserID) == nil {
		return 0, 0, domain.ErrUnauthorized
	}
	return s.TenantID, s.UserID, nil
}

// credentials valida y normaliza los datos de un usuario nuevo. El email es único en toda la plataforma.
func credentials(tenants repository.Tenants, name, email, password string) (string, string, error) {
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
	if _, u := tenants.FindByEmail(email); u != nil {
		return "", "", errors.New("ya hay una cuenta con ese email")
	}
	return name, email, nil
}

// --- Contraseñas ---

const pbkdf2Iter = 600_000 // recomendación OWASP para PBKDF2-HMAC-SHA256

func HashPassword(password string) string {
	salt := make([]byte, 16)
	rand.Read(salt)
	key, _ := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iter, 32)
	return hex.EncodeToString(salt) + ":" + hex.EncodeToString(key)
}

func CheckPassword(hash, password string) bool {
	saltHex, keyHex, ok := strings.Cut(hash, ":")
	salt, err1 := hex.DecodeString(saltHex)
	want, err2 := hex.DecodeString(keyHex)
	if !ok || err1 != nil || err2 != nil {
		return false
	}
	got, _ := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iter, 32)
	return subtle.ConstantTimeCompare(got, want) == 1
}
