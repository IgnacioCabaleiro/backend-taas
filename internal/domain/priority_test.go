package domain

import "testing"

func TestPriorityFor(t *testing.T) {
	// La matriz completa, fila por impacto y columna por urgencia (alta, media, baja).
	want := map[string][3]string{
		"alta":  {"alta", "alta", "media"},
		"media": {"alta", "media", "baja"},
		"baja":  {"media", "baja", "baja"},
	}
	for impact, row := range want {
		for k, urgency := range Priorities {
			if p, ok := PriorityFor(impact, urgency); !ok || p != row[k] {
				t.Errorf("impacto %s, urgencia %s: %q (%v), quería %q", impact, urgency, p, ok, row[k])
			}
		}
	}
	for _, c := range [][2]string{{"", "alta"}, {"alta", ""}, {"critica", "alta"}, {"", ""}} {
		if _, ok := PriorityFor(c[0], c[1]); ok {
			t.Errorf("impacto %q, urgencia %q: tendría que ser inválido", c[0], c[1])
		}
	}
}
