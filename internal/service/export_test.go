package service

import "taas-backend/internal/domain"

// Build expone Template.build a los tests (service_test), para probar plantillas armadas en el test.
func (t Template) Build(in domain.Setup) (domain.Config, error) { return t.build(in) }
