package service

import (
	"errors"
	"time"

	"taas-backend/internal/domain"
)

// Metrics arma los indicadores de la cuenta.
type Metrics struct{ base }

// SLA devuelve las métricas de SLA de los últimos days días (7, 30 o 90). Las ve quien tiene el permiso del panel,
// y solo si la cuenta tiene el módulo SLA.
func (s *Metrics) SLA(tenantID, by, days int) (domain.SLAMetrics, error) {
	if _, ok := domain.SLAPeriods[days]; !ok {
		return domain.SLAMetrics{}, errors.New("el período tiene que ser de 7, 30 o 90 días")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.tenants.Get(tenantID)
	if err != nil {
		return domain.SLAMetrics{}, err
	}
	if !t.Can(by, domain.PermPanel) {
		return domain.SLAMetrics{}, domain.ErrForbidden
	}
	if !t.Config.Modules.SLA {
		return domain.SLAMetrics{}, errors.New("el módulo de SLA no está activado en esta cuenta")
	}
	return t.ComputeSLA(days, time.Now()), nil
}
