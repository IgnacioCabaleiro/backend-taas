package service_test

import (
	"testing"

	"taas-backend/internal/domain"
	"taas-backend/internal/service"
)

// knownError arma un error conocido de la VPN con su workaround, a partir de dos incidentes.
func knownError(t *testing.T, sv *service.Services) *domain.Problem {
	t.Helper()
	a, _ := sv.Incidents.Create(tid, agent, vpn("La VPN se desconecta cada 10 minutos"))
	b, _ := sv.Incidents.Create(tid, agent, vpn("Se corta la VPN a la mañana"))
	p, err := sv.Problems.Create(tid, agent, domain.NewProblem{Title: "Caídas intermitentes de la VPN", Service: "VPN", IncidentIDs: []int{a.ID, b.ID}})
	must(t, err)
	must(t, sv.Problems.Advance(tid, agent, p.ID, domain.Advance{}))
	must(t, sv.Problems.Advance(tid, agent, p.ID, domain.Advance{RootCause: "Se agotan las licencias del concentrador", Workaround: "Conectarse por vpn2.empresa.com"}))
	return p
}

// Aplicar el workaround resuelve en un paso, cuenta el uso y, si el incidente se reabre, lo marca como fallido.
func TestApplyWorkaround(t *testing.T) {
	sv, _ := newAccount(t)
	p := knownError(t, sv)
	inc, _ := sv.Incidents.Create(tid, guest, vpn("No me conecta la VPN desde casa"))

	if sv.Incidents.ApplyWorkaround(tid, guest, inc.ID, p.ID) == nil {
		t.Fatal("quien solo reporta no aplica workarounds")
	}
	must(t, sv.Incidents.ApplyWorkaround(tid, agent, inc.ID, p.ID))
	if inc.ProblemID != p.ID || inc.ResolutionCode != "workaround" || inc.ResolutionNote != p.Workaround || inc.FirstResponseAt == nil {
		t.Fatalf("tiene que quedar vinculado, resuelto con el workaround y respondido: %+v", inc)
	}
	last := inc.History[len(inc.History)-2]
	if last.Kind != domain.EvReply {
		t.Fatalf("quien reportó tiene que recibir el workaround como respuesta: %+v", last)
	}
	if pct, ok := p.WorkaroundEffectiveness(); !ok || pct != 100 || len(p.WorkaroundUses) != 1 {
		t.Fatalf("un uso efectivo: %d%% %v", pct, p.WorkaroundUses)
	}
	if sv.Incidents.ApplyWorkaround(tid, agent, inc.ID, p.ID) == nil {
		t.Fatal("un resuelto no se vuelve a resolver")
	}

	must(t, sv.Incidents.Reopen(tid, agent, inc.ID, "sigue sin andar"))
	if pct, _ := p.WorkaroundEffectiveness(); pct != 0 || !p.WorkaroundUses[0].Failed {
		t.Fatalf("la reapertura marca el uso como fallido: %d%% %v", pct, p.WorkaroundUses)
	}

	// Resolver a mano con otro código no cuenta como uso del workaround.
	other, _ := sv.Incidents.Create(tid, agent, vpn("otra"))
	pid := p.ID
	must(t, sv.Incidents.Update(tid, agent, other.ID, domain.IncidentPatch{ProblemID: &pid}))
	resolved := "Resuelto"
	must(t, sv.Incidents.Update(tid, agent, other.ID, domain.IncidentPatch{Status: &resolved, ResolutionCode: "solucionado", ResolutionNote: "listo"}))
	if len(p.WorkaroundUses) != 1 {
		t.Fatalf("solo cuentan las resoluciones con el workaround: %v", p.WorkaroundUses)
	}

	// Un problema sin workaround publicado no se aplica.
	q, _ := sv.Problems.Create(tid, agent, domain.NewProblem{Title: "ERP lento", Service: "ERP Facturación"})
	third, _ := sv.Incidents.Create(tid, agent, vpn("tercera"))
	if sv.Incidents.ApplyWorkaround(tid, agent, third.ID, q.ID) == nil {
		t.Fatal("sin workaround no hay nada que aplicar")
	}
}

func TestUpdateWorkaroundAndDeflect(t *testing.T) {
	sv, _ := newAccount(t)
	p := knownError(t, sv)

	if sv.Problems.UpdateWorkaround(tid, guest, p.ID, "x", true) == nil {
		t.Fatal("quien solo reporta no edita workarounds")
	}
	if sv.Problems.UpdateWorkaround(tid, agent, p.ID, "", true) == nil {
		t.Fatal("no se comparte un workaround vacío")
	}
	if sv.Problems.Deflect(tid, guest, p.ID) == nil {
		t.Fatal("un workaround que no se comparte no puede evitar tickets")
	}
	must(t, sv.Problems.UpdateWorkaround(tid, agent, p.ID, "Usar vpn2.empresa.com o el 4G del celular", true))
	if !p.WorkaroundShared || p.Workaround != "Usar vpn2.empresa.com o el 4G del celular" {
		t.Fatalf("no se guardó: %+v", p)
	}

	// Quien reporta lo encuentra mientras escribe, con el workaround visible, y le alcanza.
	hits, err := sv.Knowledge.Match(tid, guest, "no puedo entrar a la vpn", "VPN")
	must(t, err)
	if len(hits) == 0 || hits[0].ProblemID != p.ID || hits[0].Workaround == "" {
		t.Fatalf("quien reporta tiene que encontrar el error conocido compartido: %+v", hits)
	}
	must(t, sv.Problems.Deflect(tid, guest, p.ID))
	if p.Deflections != 1 {
		t.Fatalf("un ticket evitado: %d", p.Deflections)
	}

	// Si deja de compartirse, quien reporta ya no lo ve.
	must(t, sv.Problems.UpdateWorkaround(tid, agent, p.ID, p.Workaround, false))
	if hits, _ := sv.Knowledge.Match(tid, guest, "no puedo entrar a la vpn", "VPN"); len(hits) != 0 {
		t.Fatalf("un workaround interno no se muestra a quien reporta: %+v", hits)
	}
	// El equipo lo sigue viendo.
	if hits, _ := sv.Knowledge.Match(tid, agent, "no puedo entrar a la vpn", "VPN"); len(hits) == 0 || hits[0].Workaround == "" {
		t.Fatalf("el equipo ve todos los errores conocidos: %+v", hits)
	}

	// Resuelto el problema, el workaround ya no se edita.
	must(t, sv.Problems.Advance(tid, agent, p.ID, domain.Advance{Solution: "Más licencias"}))
	if sv.Problems.UpdateWorkaround(tid, agent, p.ID, "x", false) == nil {
		t.Fatal("un problema resuelto no cambia su workaround")
	}
}

// Los incidentes parecidos y los errores conocidos relacionados se buscan por el texto del incidente.
func TestRelated(t *testing.T) {
	sv, _ := newAccount(t)
	p := knownError(t, sv)
	dup, _ := sv.Incidents.Create(tid, agent, vpn("La VPN se desconecta sola cada rato"))
	unrelated, _ := sv.Incidents.Create(tid, agent, domain.NewIncident{Title: "La impresora del segundo piso no imprime", Service: "Impresoras", Priority: "baja", Fields: map[string]string{"sede": "Remoto"}})

	r, err := sv.Knowledge.Related(tid, agent, dup.ID)
	must(t, err)
	if len(r.Problems) == 0 || r.Problems[0].ProblemID != p.ID {
		t.Fatalf("tiene que sugerir el error conocido de la VPN: %+v", r.Problems)
	}
	if len(r.Incidents) == 0 || r.Incidents[0].IncidentID != 1 {
		t.Fatalf("el más parecido es el #1 (se desconecta): %+v", r.Incidents)
	}
	for _, h := range r.Incidents {
		if h.IncidentID == unrelated.ID || h.IncidentID == dup.ID {
			t.Fatalf("no sugiere el propio ni uno de otro tema: %+v", r.Incidents)
		}
	}
	if _, err := sv.Knowledge.Related(tid, guest, dup.ID); err == nil {
		t.Fatal("es una herramienta de quien atiende")
	}
	if r, _ := sv.Knowledge.Related(tid, agent, unrelated.ID); len(r.Problems) != 0 {
		t.Fatalf("la impresora no tiene nada que ver con la VPN: %+v", r.Problems)
	}
}

// Valora la atención quien reportó el incidente, una sola vez y recién cuando está resuelto.
func TestRate(t *testing.T) {
	sv, _ := newAccount(t)
	inc, _ := sv.Incidents.Create(tid, guest, vpn("no anda la VPN"))
	if sv.Incidents.Rate(tid, guest, inc.ID, 5, "") == nil {
		t.Fatal("todavía no está resuelto")
	}
	resolved := "Resuelto"
	must(t, sv.Incidents.Update(tid, agent, inc.ID, domain.IncidentPatch{Status: &resolved, ResolutionCode: "solucionado", ResolutionNote: "listo"}))
	if sv.Incidents.Rate(tid, agent, inc.ID, 5, "") == nil {
		t.Fatal("valora quien lo reportó, no quien lo atendió")
	}
	if sv.Incidents.Rate(tid, guest, inc.ID, 9, "") == nil {
		t.Fatal("la valoración va de 1 a 5")
	}
	before := len(inc.History)
	must(t, sv.Incidents.Rate(tid, guest, inc.ID, 2, "  tardaron mucho "))
	if inc.Rating != 2 || inc.RatingComment != "tardaron mucho" || inc.RatedAt == nil || inc.Satisfied() || len(inc.History) != before {
		t.Fatalf("valoración mal guardada (y no es un evento del historial): %+v", inc)
	}
	if sv.Incidents.Rate(tid, guest, inc.ID, 5, "") == nil {
		t.Fatal("se valora una sola vez")
	}
}

// La revisión post-problema se escribe con el problema resuelto, y sus acciones se marcan como hechas.
func TestReview(t *testing.T) {
	sv, _ := newAccount(t)
	p := knownError(t, sv)
	review := domain.Review{WhatHappened: "Se agotaban las licencias", Learned: "Nadie miraba el uso",
		Actions: []domain.ReviewAction{{Text: "Alertar al 80% de uso", OwnerID: agent, Due: "2026-12-01"}, {Text: "  "}}}
	if sv.Problems.SaveReview(tid, agent, p.ID, review) == nil {
		t.Fatal("la revisión es para problemas resueltos")
	}
	must(t, sv.Problems.Advance(tid, agent, p.ID, domain.Advance{Solution: "Más licencias"}))
	if sv.Problems.SaveReview(tid, agent, p.ID, domain.Review{}) == nil {
		t.Fatal("hay que contar qué pasó")
	}
	bad := review
	bad.Actions = []domain.ReviewAction{{Text: "x", Due: "mañana"}}
	if sv.Problems.SaveReview(tid, agent, p.ID, bad) == nil {
		t.Fatal("la fecha tiene que ser AAAA-MM-DD")
	}
	if sv.Problems.SaveReview(tid, guest, p.ID, review) == nil {
		t.Fatal("quien solo reporta no escribe revisiones")
	}
	must(t, sv.Problems.SaveReview(tid, agent, p.ID, review))
	if p.Review == nil || len(p.Review.Actions) != 1 || p.Review.UserID != agent {
		t.Fatalf("las acciones vacías se descartan: %+v", p.Review)
	}
	must(t, sv.Problems.SetActionDone(tid, agent, p.ID, 0, true))
	if !p.Review.Actions[0].Done || sv.Problems.SetActionDone(tid, agent, p.ID, 5, true) == nil {
		t.Fatal("la acción 0 queda hecha y la 5 no existe")
	}
}
