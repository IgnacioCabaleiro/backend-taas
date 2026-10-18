package repository

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

// MemorySessions guarda las sesiones en memoria.
//
// ponytail: sin vencimiento y se pierden al reiniciar (hay que volver a entrar).
// Pasar a Redis o a una tabla con expiración antes de exponerlo a internet.
type MemorySessions struct {
	mu      sync.Mutex
	byToken map[string]Session
}

func NewMemorySessions() *MemorySessions { return &MemorySessions{byToken: map[string]Session{}} }

func (m *MemorySessions) Create(s Session) string {
	b := make([]byte, 32)
	rand.Read(b)
	token := hex.EncodeToString(b)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byToken[token] = s
	return token
}

func (m *MemorySessions) Get(token string) (Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.byToken[token]
	return s, ok
}

func (m *MemorySessions) Delete(token string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.byToken, token)
}

func (m *MemorySessions) DeleteUser(tenantID, userID int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for token, s := range m.byToken {
		if s.TenantID == tenantID && s.UserID == userID {
			delete(m.byToken, token)
		}
	}
}
