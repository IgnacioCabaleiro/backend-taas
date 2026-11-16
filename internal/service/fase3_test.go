package service_test

import (
	"testing"
	"time"

	"taas-backend/internal/domain"
	"taas-backend/internal/repository"
	"taas-backend/internal/seed"
	"taas-backend/internal/service"
)

// El simulador recalcula el cumplimiento con otros plazos, respetando lo que cada incidente estuvo en pausa.
func TestSimulateSLA(t *testing.T) {
	sv, tn := newAccount(t)
	now := time.Now()
	// Dos incidentes de prioridad alta (SLA de la plantilla IT), resueltos a las 3 h y a las 6 h de registrarse.
	sla := tn.Config.SLAHours["alta"]
	for _, h := range []int{sla - 1, sla + 2} {
		inc, _ := sv.Incidents.Create(tid, agent, vpn("x"))
		inc.CreatedAt = now.Add(-48 * time.Hour)
		due := inc.CreatedAt.Add(time.Duration(sla) * time.Hour)
		done := inc.CreatedAt.Add(time.Duration(h) * time.Hour)
		inc.DueAt, inc.ResolvedAt = &due, &done
		inc.ResponseDueAt = nil // solo se mide la resolución
	}
	hours := func(alta int) map[string]int { return map[string]int{"alta": alta, "media": 8, "baja": 24} }

	same, err := sv.Metrics.SimulateSLA(tid, owner, domain.SimInput{Days: 7, SLAHours: hours(sla), ResponseHours: hours(sla)})
	must(t, err)
	if r := same.Total.Resolution; r.Measured != 2 || r.Late != 1 || r.SimLate != 1 || len(same.Changes) != 0 {
		t.Fatalf("con los mismos plazos el resultado es el real: %+v %+v", r, same.Changes)
	}
	looser, _ := sv.Metrics.SimulateSLA(tid, owner, domain.SimInput{Days: 7, SLAHours: hours(sla + 3), ResponseHours: hours(sla)})
	if r := looser.Total.Resolution; r.Late != 1 || r.SimLate != 0 || len(looser.Changes) != 1 || !looser.Changes[0].WasLate {
		t.Fatalf("con un plazo más largo, el vencido entra en plazo: %+v %+v", r, looser.Changes)
	}
	tighter, _ := sv.Metrics.SimulateSLA(tid, owner, domain.SimInput{Days: 7, SLAHours: hours(1), ResponseHours: hours(sla)})
	if tighter.Total.Resolution.SimLate != 2 {
		t.Fatalf("con 1 h, los dos quedan fuera: %+v", tighter.Total.Resolution)
	}

	// Lo que estuvo en pausa se respeta: con 1 h de pausa, resolver a las sla+0.5 h está en plazo.
	inc, _ := sv.Incidents.Create(tid, agent, vpn("pausado"))
	inc.CreatedAt = now.Add(-24 * time.Hour)
	due := inc.CreatedAt.Add(time.Duration(sla)*time.Hour + time.Hour)
	done := inc.CreatedAt.Add(time.Duration(sla)*time.Hour + 30*time.Minute)
	inc.DueAt, inc.ResolvedAt = &due, &done
	inc.ResponseDueAt = nil
	r, _ := sv.Metrics.SimulateSLA(tid, owner, domain.SimInput{Days: 7, SLAHours: hours(sla), ResponseHours: hours(sla)})
	if r.Total.Resolution.SimLate != 1 {
		t.Fatalf("la pausa tiene que seguir descontándose: %+v", r.Total.Resolution)
	}

	if _, err := sv.Metrics.SimulateSLA(tid, owner, domain.SimInput{Days: 5, SLAHours: hours(1), ResponseHours: hours(1)}); err == nil {
		t.Fatal("el período tiene que ser 7, 30 o 90")
	}
	if _, err := sv.Metrics.SimulateSLA(tid, owner, domain.SimInput{Days: 7, SLAHours: hours(0), ResponseHours: hours(1)}); err == nil {
		t.Fatal("los plazos tienen que ser de al menos 1 hora")
	}
	if _, err := sv.Metrics.SimulateSLA(tid, guest, domain.SimInput{Days: 7, SLAHours: hours(1), ResponseHours: hours(1)}); err == nil {
		t.Fatal("quien no ve el panel no simula")
	}
}

func TestSwarm(t *testing.T) {
	sv, _ := newAccount(t)
	p := knownError(t, sv)
	if sv.Problems.SwarmNote(tid, agent, p.ID, "pista", "x") == nil {
		t.Fatal("sin swarming abierto no hay aportes")
	}
	if sv.Problems.StartSwarm(tid, guest, p.ID) == nil {
		t.Fatal("quien solo reporta no abre un swarming")
	}
	must(t, sv.Problems.StartSwarm(tid, agent, p.ID))
	if sv.Problems.StartSwarm(tid, owner, p.ID) == nil {
		t.Fatal("ya hay uno en curso")
	}
	must(t, sv.Problems.SwarmNote(tid, owner, p.ID, "hallazgo", "  las licencias  "))
	if sv.Problems.SwarmNote(tid, owner, p.ID, "opinión", "x") == nil || sv.Problems.SwarmNote(tid, owner, p.ID, "pista", " ") == nil {
		t.Fatal("tipo inválido o aporte vacío")
	}
	must(t, sv.Problems.JoinSwarm(tid, owner, p.ID))
	if s := p.Swarm; len(s.Members) != 2 || len(s.Notes) != 1 || s.Notes[0].Text != "las licencias" {
		t.Fatalf("aportar suma al equipo una sola vez: %+v", s)
	}
	must(t, sv.Problems.CloseSwarm(tid, agent, p.ID))
	if p.Swarm.Active() || sv.Problems.SwarmNote(tid, agent, p.ID, "pista", "tarde") == nil {
		t.Fatal("cerrado, ya no recibe aportes")
	}
	must(t, sv.Problems.StartSwarm(tid, agent, p.ID)) // se puede volver a abrir
}

// El radar agrupa el mismo síntoma en servicios distintos, y no mezcla síntomas que no tienen que ver.
func TestCrossServiceRecurrence(t *testing.T) {
	sv, tn := newAccount(t)
	in := func(service, title string) domain.NewIncident {
		return domain.NewIncident{Title: title, Service: service, Priority: "alta", Fields: map[string]string{"sede": "Remoto"}}
	}
	sv.Incidents.Create(tid, agent, in("WiFi", "Sin red en el segundo piso desde el corte de luz"))
	sv.Incidents.Create(tid, agent, in("Impresoras", "La impresora del segundo piso no responde desde el corte de luz"))
	sv.Incidents.Create(tid, agent, in("Correo", "No llegan los mails de los proveedores"))
	sv.Incidents.Create(tid, agent, in("WiFi", "El WiFi de recepción anda lento"))

	got := tn.CrossServiceRecurrence(time.Now())
	if len(got) != 1 || len(got[0].IncidentIDs) != 2 || len(got[0].Services) != 2 || !domain.Has(got[0].Terms, "corte") {
		t.Fatalf("esperaba un grupo WiFi + Impresoras por el corte de luz: %+v", got)
	}
	// Abrir el problema desde el radar los saca del radar.
	_, err := sv.Problems.Create(tid, agent, domain.NewProblem{Title: "Corte de luz", Service: "WiFi", IncidentIDs: got[0].IncidentIDs})
	must(t, err)
	if got := tn.CrossServiceRecurrence(time.Now()); len(got) != 0 {
		t.Fatalf("ya investigado, sale del radar: %+v", got)
	}
	if v, _ := sv.State.View(tid, agent); v.CrossSuggestions == nil {
		t.Fatal("la vista siempre trae la lista, aunque esté vacía")
	}
}

// Los errores conocidos del rubro se precargan si se pide y hay gestión de problemas, solo para los servicios
// que quedaron en el catálogo, y repetir el onboarding los reemplaza.
func TestPreloadKnownErrors(t *testing.T) {
	tenants, _ := repository.NewJSONTenants("")
	sv := service.New(tenants, repository.NewMemorySessions(), repository.NewMemoryBlobs())
	sv.Auth.Signup("Nueva", "Titular", "precarga@demo.test", pass)
	tn, _ := tenants.Get(tid)

	must(t, sv.Setup.Onboard(tid, owner, domain.Setup{Template: "it", KnownErrors: true}))
	if len(tn.Problems) != 0 {
		t.Fatal("sin gestión de problemas no se precarga nada")
	}
	setup := domain.Setup{Template: "it", Problems: true, KnownErrors: true, Services: []string{"VPN", "Impresoras", "Otro servicio"}}
	must(t, sv.Setup.Onboard(tid, owner, setup))
	if len(tn.Problems) != 2 || !tn.Problems[0].Preloaded || tn.Problems[0].Status != domain.ErrorConocido || tn.Problems[1].ID != 2 {
		t.Fatalf("dos errores conocidos (Correo ya no está en el catálogo): %+v", tn.Problems)
	}
	must(t, sv.Setup.Onboard(tid, owner, setup))
	if len(tn.Problems) != 2 {
		t.Fatalf("repetir el onboarding no los duplica: %d", len(tn.Problems))
	}
	setup.KnownErrors = false
	must(t, sv.Setup.Onboard(tid, owner, setup))
	if len(tn.Problems) != 0 {
		t.Fatal("si se destilda, se quitan")
	}
	for _, tpl := range sv.Setup.Templates() {
		for _, k := range tpl.KnownErrors {
			if !domain.Has(tpl.Config.Services, k.Service) || k.Workaround == "" || k.RootCause == "" {
				t.Errorf("%s: error conocido inválido %+v", tpl.Key, k)
			}
		}
	}
}

// La demo arranca con un swarming en curso y con el mismo síntoma en servicios distintos.
func TestDemoFase3(t *testing.T) {
	for _, tn := range seed.Tenants() {
		if len(tn.CrossServiceRecurrence(time.Now())) == 0 {
			t.Errorf("%s: el radar tiene que encontrar el corte de luz", tn.Name)
		}
		swarms := 0
		for _, p := range tn.Problems {
			if p.Swarm.Active() && len(p.Swarm.Notes) > 0 {
				swarms++
			}
		}
		if swarms != 1 {
			t.Errorf("%s: un swarming en curso, hay %d", tn.Name, swarms)
		}
		if _, err := tn.SimulateSLA(domain.SimInput{Days: 90, SLAHours: tn.Config.SLAHours, ResponseHours: tn.Config.ResponseHours}, time.Now()); err != nil {
			t.Errorf("%s: %v", tn.Name, err)
		}
	}
}

// Invitar: la persona elige su contraseña con el link; hasta entonces no entra, y el link solo lo ve quien configura.
func TestInvite(t *testing.T) {
	sv, tn := newAccount(t)
	if sv.Accounts.Invite(tid, agent, "Nueva", "nueva@demo.test", 2) == nil {
		t.Fatal("solo invita quien configura")
	}
	must(t, sv.Accounts.Invite(tid, owner, "Nueva Persona", "NUEVA@demo.test", 2))
	u := tn.Users[len(tn.Users)-1]
	if u.Invite == "" || u.Hash != "" || u.Email != "nueva@demo.test" {
		t.Fatalf("queda la invitación pendiente: %+v", u)
	}
	if sv.Accounts.Invite(tid, owner, "Otra", "nueva@demo.test", 2) == nil {
		t.Fatal("el email sigue siendo único")
	}
	if _, err := sv.Auth.Login("nueva@demo.test", pass); err == nil {
		t.Fatal("sin aceptar la invitación no se entra")
	}
	if v, _ := sv.State.View(tid, agent); v.Users[len(v.Users)-1].Invite != "" {
		t.Fatal("el link es del titular")
	}
	if v, _ := sv.State.View(tid, owner); v.Users[len(v.Users)-1].Invite == "" {
		t.Fatal("el titular ve el link para copiarlo")
	}
	inv, err := sv.Auth.Invitation(u.Invite)
	if err != nil || inv.Name != "Nueva Persona" || inv.Company != "Demo" {
		t.Fatalf("datos de la invitación: %+v %v", inv, err)
	}
	if _, err := sv.Auth.AcceptInvite(u.Invite, "corta"); err == nil {
		t.Fatal("la contraseña necesita 8 caracteres")
	}
	token, err := sv.Auth.AcceptInvite(u.Invite, "una-clave-nueva")
	must(t, err)
	if gotT, gotU, err := sv.Auth.Authenticate(token); err != nil || gotT != tid || gotU != u.ID {
		t.Fatalf("aceptar abre la sesión: %d %d %v", gotT, gotU, err)
	}
	if _, err := sv.Auth.Login("nueva@demo.test", "una-clave-nueva"); err != nil {
		t.Fatal("ya puede entrar con su contraseña")
	}
	if _, err := sv.Auth.AcceptInvite(u.Invite, "una-clave-nueva"); err == nil {
		t.Fatal("la invitación se usa una sola vez")
	}
}
