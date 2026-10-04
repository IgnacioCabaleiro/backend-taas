package seed

import (
	"testing"

	"taas-backend/internal/domain"
	"taas-backend/internal/service"
)

// La demo tiene que arrancar con datos coherentes y una sugerencia por cuenta.
func TestTenants(t *testing.T) {
	for _, tn := range Tenants() {
		if len(service.Suggestions(tn)) == 0 {
			t.Errorf("%s: sin sugerencias de recurrencia", tn.Name)
		}
		if !service.CheckPassword(tn.Users[0].Hash, DemoPassword) || tn.Users[0].Email == "" {
			t.Errorf("%s: el titular no puede entrar con la contraseña de demo", tn.Name)
		}
		for _, inc := range tn.Incidents {
			if (inc.ResolvedAt != nil) != (inc.Status == tn.ClosedState()) {
				t.Errorf("%s #%d: estado %q no coincide con resolvedAt", tn.Name, inc.ID, inc.Status)
			}
		}
		for _, p := range tn.Problems {
			for _, inc := range tn.Incidents {
				if inc.ProblemID == p.ID && p.Status == domain.Resuelto && inc.ResolvedAt == nil {
					t.Errorf("%s P-%d resuelto con #%d abierto", tn.Name, p.ID, inc.ID)
				}
			}
		}
	}
}
