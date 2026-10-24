package seed

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"taas-backend/internal/domain"
	"taas-backend/internal/service"
)

// La demo tiene que arrancar con datos coherentes y una sugerencia por cuenta.
func TestTenants(t *testing.T) {
	reopened, bySolution := 0, 0
	for _, tn := range Tenants() {
		if len(service.Suggestions(tn)) == 0 {
			t.Errorf("%s: sin sugerencias de recurrencia", tn.Name)
		}
		if !service.CheckPassword(tn.Users[0].Hash, DemoPassword) || tn.Users[0].Email == "" {
			t.Errorf("%s: el titular no puede entrar con la contraseña de demo", tn.Name)
		}
		for _, inc := range tn.Incidents {
			if (inc.ResolvedAt != nil) != (inc.Status == tn.ResolvedState()) {
				t.Errorf("%s #%d: estado %q no coincide con resolvedAt", tn.Name, inc.ID, inc.Status)
			}
		}
		for _, p := range tn.Problems {
			for _, inc := range tn.Incidents {
				if inc.ProblemID == p.ID && p.Status == domain.Resuelto && inc.ResolvedAt == nil {
					t.Errorf("%s P-%d resuelto con #%d abierto", tn.Name, p.ID, inc.ID)
				}
				if inc.ProblemID == p.ID && p.Status == domain.Resuelto && inc.ResolutionNote == p.Solution {
					bySolution++
				}
			}
		}
		for _, inc := range tn.Incidents {
			if strings.HasPrefix(inc.ResolutionNote, "Resuelto por el problema") {
				t.Errorf("%s #%d: la nota de resolución nombra al problema: %q", tn.Name, inc.ID, inc.ResolutionNote)
			}
		}
		reopened += checkLifecycle(t, tn)
	}
	if reopened == 0 {
		t.Error("la demo tiene que mostrar algún incidente reabierto")
	}
	// Los incidentes que resuelve un problema llevan solo la solución como nota, como en la API.
	if bySolution == 0 {
		t.Error("la demo tiene que mostrar algún incidente resuelto por su problema")
	}
}

// checkLifecycle verifica el ciclo de vida de los incidentes de una cuenta de demo y devuelve cuántos se reabrieron.
func checkLifecycle(t *testing.T, tn *domain.Tenant) (reopened int) {
	t.Helper()
	now := time.Now()
	kinds := []string{domain.EvCreated, domain.EvStatus, domain.EvAssigned, domain.EvProblem, domain.EvReply,
		domain.EvNote, domain.EvResolved, domain.EvReopened, domain.EvClosed, domain.EvRelated}
	c := tn.Config
	if err := c.Validate(); err != nil {
		t.Errorf("%s: configuración inválida: %v", tn.Name, err)
	}
	if fmt.Sprint(c.PauseStates) != "["+service.WaitingRequester+"]" || c.States[len(c.States)-2] != service.WaitingRequester {
		t.Errorf("%s: %q tiene que ser el único estado de pausa, justo antes del de resolución: %v %v", tn.Name, service.WaitingRequester, c.States, c.PauseStates)
	}
	autoClose := time.Duration(c.AutoCloseDays) * 24 * time.Hour
	var responded, replies, notes, paused, onTime, resolved, held, byHand, auto, discarded, related int
	for _, inc := range tn.Incidents {
		h := inc.History
		if len(h) == 0 || h[0].Kind != domain.EvCreated || !h[0].At.Equal(inc.CreatedAt) {
			t.Errorf("%s #%d: el historial no arranca con el registro", tn.Name, inc.ID)
			continue
		}
		// Se recorre el historial siguiendo el estado y la etapa: tiene que terminar donde está el incidente.
		status, closedAt, firstReply, lastResolved := c.States[0], (*time.Time)(nil), (*time.Time)(nil), ""
		for k, e := range h {
			if !domain.Has(kinds, e.Kind) || e.At.After(now) || (k > 0 && e.At.Before(h[k-1].At)) {
				t.Errorf("%s #%d: evento inválido o fuera de orden: %+v", tn.Name, inc.ID, e)
			}
			if closedAt != nil && e.Kind != domain.EvRelated && e.Kind != domain.EvProblem {
				t.Errorf("%s #%d: evento %q después del cierre", tn.Name, inc.ID, e.Kind)
			}
			switch e.Kind {
			case domain.EvStatus, domain.EvResolved, domain.EvReopened:
				rest, ok := strings.CutPrefix(e.Text, "De "+status+" a ")
				if !ok {
					t.Errorf("%s #%d: %q no sale del estado %q", tn.Name, inc.ID, e.Text, status)
				}
				status, _, _ = strings.Cut(rest, " · ")
				status, _, _ = strings.Cut(status, ":")
				if e.Kind == domain.EvResolved {
					lastResolved = e.Text
				}
				if (e.Kind == domain.EvResolved) != (status == tn.ResolvedState()) {
					t.Errorf("%s #%d: solo la resolución pasa al estado de resolución: %+v", tn.Name, inc.ID, e)
				}
				if e.Kind == domain.EvReopened && !tn.Can(e.UserID, domain.PermResolve) {
					t.Errorf("%s #%d: reabrió alguien sin permiso resolver", tn.Name, inc.ID)
				}
			case domain.EvReply:
				replies++
				if firstReply == nil && tn.Can(e.UserID, domain.PermResolve) {
					firstReply = &e.At
				}
			case domain.EvNote:
				notes++
			case domain.EvClosed:
				closedAt = &e.At
				// Ningún cierre después de autoCloseDays sin actividad; el automático, justo a esa altura.
				idle := h[k-1].At.Add(autoClose)
				if e.UserID == 0 {
					auto++
					if !e.At.Equal(idle) || e.Text != fmt.Sprintf("Cerrado automáticamente tras %d días sin actividad", c.AutoCloseDays) {
						t.Errorf("%s #%d: cierre automático a %v, esperaba %v (último evento + %d días)", tn.Name, inc.ID, e.At, idle, c.AutoCloseDays)
					}
				} else if e.At.After(idle) {
					t.Errorf("%s #%d: cerrado a mano después de que debía cerrarse solo", tn.Name, inc.ID)
				} else if !domain.Has(domain.ClosingCodes, inc.ResolutionCode) {
					byHand++
				}
			case domain.EvRelated:
				if closedAt == nil {
					t.Errorf("%s #%d: relacionado sin estar cerrado", tn.Name, inc.ID)
				}
			}
		}
		if status != inc.Status {
			t.Errorf("%s #%d: el historial termina en %q y el incidente está en %q", tn.Name, inc.ID, status, inc.Status)
		}
		if (closedAt == nil) != (inc.ClosedAt == nil) || (closedAt != nil && !closedAt.Equal(*inc.ClosedAt)) {
			t.Errorf("%s #%d: closedAt=%v no coincide con el historial", tn.Name, inc.ID, inc.ClosedAt)
		}
		// La primera respuesta es la primera respuesta pública de quien atiende.
		if (firstReply == nil) != (inc.FirstResponseAt == nil) || (firstReply != nil && !firstReply.Equal(*inc.FirstResponseAt)) {
			t.Errorf("%s #%d: firstResponseAt=%v no es la primera respuesta de quien atiende", tn.Name, inc.ID, inc.FirstResponseAt)
		}
		if inc.FirstResponseAt != nil {
			responded++
		}

		switch {
		case inc.ResolvedAt == nil:
			if inc.ResolutionCode != "" || inc.ResolutionNote != "" || inc.ClosedAt != nil || inc.AutoCloseHold {
				t.Errorf("%s #%d: abierto con resolución, cierre o retención", tn.Name, inc.ID)
			}
		case !domain.Has(domain.ResolutionCodes, inc.ResolutionCode) || inc.ResolutionNote == "":
			t.Errorf("%s #%d: resuelto sin código o sin nota", tn.Name, inc.ID)
		case !strings.HasSuffix(lastResolved, " · "+domain.ResolutionLabels[inc.ResolutionCode]+": "+inc.ResolutionNote):
			t.Errorf("%s #%d: la resolución %q no lleva la etiqueta de %q y la nota", tn.Name, inc.ID, lastResolved, inc.ResolutionCode)
		case domain.Has(domain.ClosingCodes, inc.ResolutionCode):
			discarded++
			if inc.ClosedAt == nil || !inc.ClosedAt.Equal(*inc.ResolvedAt) {
				t.Errorf("%s #%d: duplicado o cancelado cierra al resolver", tn.Name, inc.ID)
			}
		case inc.ClosedAt == nil:
			resolved++
			if !inc.AutoCloseHold && !h[len(h)-1].At.Add(autoClose).After(now) {
				t.Errorf("%s #%d: resuelto sin actividad hace más de %d días sin cerrarse solo", tn.Name, inc.ID, c.AutoCloseDays)
			}
		case inc.ClosedAt.Before(*inc.ResolvedAt):
			t.Errorf("%s #%d: cerrado antes de resolverse", tn.Name, inc.ID)
		}
		if inc.AutoCloseHold {
			held++
		}
		// El vencimiento de la primera respuesta se fija al registrar y la pausa no lo mueve.
		if want := inc.CreatedAt.Add(time.Duration(c.ResponseHours[inc.Priority]) * time.Hour); inc.ResponseDueAt == nil || !inc.ResponseDueAt.Equal(want) {
			t.Errorf("%s #%d: responseDueAt=%v, esperaba %v", tn.Name, inc.ID, inc.ResponseDueAt, want)
		}
		inPause := inc.ResolvedAt == nil && domain.Has(c.PauseStates, inc.Status)
		if (inc.PausedAt != nil) != inPause {
			t.Errorf("%s #%d: pausedAt=%v no coincide con el estado %q", tn.Name, inc.ID, inc.PausedAt, inc.Status)
		}
		if inc.PausedAt != nil {
			paused++
			if inc.DueAt.After(*inc.PausedAt) {
				onTime++
			}
		}
		if inc.ReopenCount > 0 {
			reopened++
		}
		if inc.RelatedIncidentID != 0 {
			related++
			old, err := tn.Incident(inc.RelatedIncidentID)
			if err != nil || old.ClosedAt == nil || old.ClosedAt.After(inc.CreatedAt) ||
				old.History[len(old.History)-1].Text != fmt.Sprintf("Se registró #%d relacionado", inc.ID) {
				t.Errorf("%s #%d: relacionado con un incidente que no estaba cerrado o sin el evento", tn.Name, inc.ID)
			}
		}
	}
	if responded*2 <= len(tn.Incidents) || replies == 0 || notes == 0 {
		t.Errorf("%s: la mayoría tiene que tener primera respuesta, y tiene que haber respuestas y notas (%d/%d, %d, %d)",
			tn.Name, responded, len(tn.Incidents), replies, notes)
	}
	if onTime == 0 {
		t.Errorf("%s: tiene que haber un incidente en pausa todavía dentro del plazo (dueAt > pausedAt)", tn.Name)
	}
	if c.AutoCloseDays != domain.DefaultAutoCloseDays {
		t.Errorf("%s: autoCloseDays=%d, esperaba el de las plantillas", tn.Name, c.AutoCloseDays)
	}
	if paused == 0 || resolved == 0 || held == 0 || byHand == 0 || auto == 0 || discarded < 1 || discarded > 2 || related == 0 {
		t.Errorf("%s: la demo tiene que mostrar cada etapa: pausados=%d resueltos=%d retenidos=%d cerrados a mano=%d solos=%d duplicados o cancelados=%d relacionados=%d",
			tn.Name, paused, resolved, held, byHand, auto, discarded, related)
	}
	return reopened
}
