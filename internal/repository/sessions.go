package repository

import (
	"crypto/rand"
	"encoding/hex"
)

// MemorySessions guarda las sesiones en memoria.
//
// ponytail: sin vencimiento y se pierden al reiniciar (hay que volver a entrar).
// Pasar a Redis o a una tabla con expiración antes de exponerlo a internet.
type MemorySessions struct{ byToken map[string]Session }

func NewMemorySessions() *MemorySessions { return &MemorySessions{byToken: map[string]Session{}} }

func (m *MemorySessions) Create(s Session) string {
	b := make([]byte, 32)
	rand.Read(b)
	token := hex.EncodeToString(b)
	m.byToken[token] = s
	return token
}

func (m *MemorySessions) Get(token string) (Session, bool) {
	s, ok := m.byToken[token]
	return s, ok
}

func (m *MemorySessions) Delete(token string) { delete(m.byToken, token) }

func (m *MemorySessions) DeleteUser(tenantID, userID int) {
	for token, s := range m.byToken {
		if s.TenantID == tenantID && s.UserID == userID {
			delete(m.byToken, token)
		}
	}
}
