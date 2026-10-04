// Package repository guarda y recupera cuentas y sesiones. Los servicios dependen de estas
// interfaces, no de dónde viven los datos: hoy un archivo JSON, mañana MySQL.
package repository

import "taas-backend/internal/domain"

type Tenants interface {
	Get(id int) (*domain.Tenant, error)
	// FindByEmail busca un usuario en toda la plataforma: el email es único entre cuentas.
	FindByEmail(email string) (*domain.Tenant, *domain.User)
	// Create asigna el ID de la cuenta y la guarda.
	Create(t *domain.Tenant) error
	Save(t *domain.Tenant) error
}

// Session: quién está detrás de un token.
type Session struct{ TenantID, UserID int }

type Sessions interface {
	Create(s Session) (token string)
	Get(token string) (Session, bool)
	Delete(token string)
	// DeleteUser cierra todas las sesiones de un usuario (cuando se lo elimina).
	DeleteUser(tenantID, userID int)
}
