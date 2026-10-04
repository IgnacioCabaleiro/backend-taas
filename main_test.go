package main

import "testing"

// Una cuenta recién contratada y onboardeada (IT, complejidad completa): 1 titular, 2 agente, 3 solicitante.
func newTenant(t *testing.T) (*Store, *Tenant) {
	s := &Store{sessions: map[string]session{}}
	if _, err := s.Signup("Demo", "Titular", "titular@demo.test", "clave-de-prueba"); err != nil {
		t.Fatal(err)
	}
	tn := s.Tenants[0]
	setup := full
	setup.Template = "it"
	if err := tn.Onboard(setup, 1); err != nil {
		t.Fatal(err)
	}
	if err := tn.FinishOnboarding(1); err != nil {
		t.Fatal(err)
	}
	if err := s.AddUser(tn, "Agente", "agente@demo.test", "clave-de-prueba", 2, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.AddUser(tn, "Solicitante", "solicitante@demo.test", "clave-de-prueba", 3, 1); err != nil {
		t.Fatal(err)
	}
	return s, tn
}

func vpn(title string) NewIncident {
	return NewIncident{Title: title, Service: "VPN", Priority: "alta", Fields: map[string]string{"sede": "Remoto"}}
}

// Flujo completo: incidentes recurrentes -> problema -> error conocido -> solución que cierra los incidentes.
func TestProblemLifecycle(t *testing.T) {
	_, tn := newTenant(t)
	a, err := tn.CreateIncident(vpn("no anda"), 2)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := tn.CreateIncident(vpn("no anda de nuevo"), 2)
	if a.Status != "Abierto" || a.DueAt.Sub(a.CreatedAt).Hours() != 4 {
		t.Fatalf("estado inicial o SLA mal: %s %v", a.Status, a.DueAt.Sub(a.CreatedAt))
	}

	if sg := tn.Suggestions(); len(sg) != 1 || len(sg[0].IncidentIDs) != 2 {
		t.Fatalf("esperaba 1 sugerencia con 2 incidentes, obtuve %+v", sg)
	}
	p, err := tn.CreateProblem("VPN inestable", "", "VPN", []int{a.ID, b.ID}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(tn.Suggestions()) != 0 {
		t.Fatal("los incidentes ya vinculados no deben sugerirse")
	}

	if err := tn.Advance(p.ID, Advance{}, 2); err != nil || p.Status != EnAnalisis {
		t.Fatalf("status=%s err=%v", p.Status, err)
	}
	if tn.Advance(p.ID, Advance{}, 2) == nil {
		t.Fatal("no debe ser error conocido sin causa raíz")
	}
	if err := tn.Advance(p.ID, Advance{RootCause: "licencias", Workaround: "vpn2"}, 2); err != nil || p.Status != ErrorConocido {
		t.Fatalf("status=%s err=%v", p.Status, err)
	}
	if tn.Advance(p.ID, Advance{}, 2) == nil {
		t.Fatal("no debe resolverse sin solución")
	}
	if err := tn.Advance(p.ID, Advance{Solution: "más licencias"}, 2); err != nil || p.Status != Resuelto {
		t.Fatalf("status=%s err=%v", p.Status, err)
	}
	if a.ResolvedAt == nil || b.Status != "Resuelto" {
		t.Fatal("resolver el problema debe cerrar sus incidentes")
	}
	if tn.UpdateIncident(a.ID, IncidentPatch{ProblemID: &p.ID}, 2) == nil || tn.UpdateIncident(99, IncidentPatch{}, 2) == nil {
		t.Fatal("no se puede vincular a un problema resuelto ni tocar un incidente inexistente")
	}
}

// La configuración es de cada cuenta: valida campos propios y migra estados renombrados o eliminados.
func TestConfig(t *testing.T) {
	_, tn := newTenant(t)
	if _, err := tn.CreateIncident(NewIncident{Title: "x", Service: "VPN", Priority: "alta"}, 2); err == nil {
		t.Fatal("Sede es obligatoria")
	}
	if _, err := tn.CreateIncident(NewIncident{Title: "x", Service: "Tomógrafo", Priority: "alta"}, 2); err == nil {
		t.Fatal("el servicio tiene que ser del catálogo")
	}
	inc, _ := tn.CreateIncident(vpn("x"), 2)
	enCurso := "En curso"
	if err := tn.UpdateIncident(inc.ID, IncidentPatch{Status: &enCurso}, 2); err != nil {
		t.Fatal(err)
	}

	c, _ := templates["it"].build(full)
	c.States[1] = "Trabajando"
	if tn.SetConfig(c, 2) == nil {
		t.Fatal("un agente no puede cambiar la configuración")
	}
	if err := tn.SetConfig(c, 1); err != nil || inc.Status != "Trabajando" {
		t.Fatalf("renombrar un estado debe arrastrar sus incidentes: %q %v", inc.Status, err)
	}
	c, _ = templates["it"].build(full)
	c.States = []string{"Nuevo", "Cerrado"}
	c.SLAHours["alta"] = 2
	if err := tn.SetConfig(c, 1); err != nil {
		t.Fatal(err)
	}
	if inc.Status != "Nuevo" {
		t.Fatalf("un estado eliminado debe volver al inicial, quedó %q", inc.Status)
	}
	next, _ := tn.CreateIncident(vpn("y"), 2)
	if next.DueAt.Sub(next.CreatedAt).Hours() != 2 {
		t.Fatal("el nuevo SLA debe aplicarse a los incidentes nuevos")
	}
	c.States = []string{"Único"}
	if tn.SetConfig(c, 1) == nil {
		t.Fatal("hacen falta al menos dos estados")
	}
}

// Las respuestas del onboarding definen la configuración inicial de la cuenta.
func TestOnboarding(t *testing.T) {
	s := &Store{sessions: map[string]session{}}
	s.Signup("Chica", "Titular", "chica@demo.test", "clave-de-prueba")
	tn := s.Tenants[0]
	if tn.Onboarded || tn.FinishOnboarding(1) == nil {
		t.Fatal("una cuenta nueva arranca sin onboarding y no se puede terminar sin configurar")
	}
	if tn.Onboard(Setup{Template: "otro"}, 1) == nil || tn.Onboard(Setup{Template: "nada"}, 1) == nil {
		t.Fatal("el rubro 'otro' exige describirlo, y el rubro tiene que existir")
	}

	// Trabaja solo, sin plazos ni recurrencias: lo mínimo.
	if err := tn.Onboard(Setup{Template: "salud"}, 1); err != nil {
		t.Fatal(err)
	}
	c := tn.Config
	if len(c.States) != 2 || len(c.Fields) != 0 || c.Modules.SLA || c.Modules.Problems || tn.Industry != "Salud" {
		t.Fatalf("sin equipo, plazos ni datos propios no debe haber flujo, SLA, campos ni problemas: %+v", c)
	}

	// Vuelve atrás y cambia las respuestas: rubro propio, servicios ajustados, con plazos y equipo.
	err := tn.Onboard(Setup{Template: "otro", Industry: " Estudio contable ", Services: []string{"Sueldos", " Impuestos ", ""}, Team: true, SLA: true}, 1)
	if err != nil {
		t.Fatal(err)
	}
	c = tn.Config
	if tn.Industry != "Estudio contable" || len(c.Services) != 2 || c.Services[1] != "Impuestos" || len(c.States) != 3 || !c.Modules.SLA {
		t.Fatalf("no se aplicaron las respuestas: %q %+v", tn.Industry, c)
	}
	if tn.Onboard(Setup{Template: "it", Services: []string{"", " "}}, 1) == nil || tn.Industry != "Estudio contable" {
		t.Fatal("sin ningún servicio no hay ticketera: debe fallar sin tocar lo ya configurado")
	}

	tn.Onboard(Setup{Template: "salud"}, 1)
	inc, err := tn.CreateIncident(NewIncident{Title: "primer ticket", Service: tn.Config.Services[0], Priority: "alta"}, 1)
	if err != nil || inc.DueAt != nil {
		t.Fatalf("el primer ticket se crea durante el onboarding; sin SLA no hay vencimiento: %v %v", inc, err)
	}
	if _, err := tn.CreateProblem("p", "", tn.Config.Services[0], nil, 1); err == nil {
		t.Fatal("sin el módulo no se pueden crear problemas")
	}
	if tn.FinishOnboarding(1) != nil || !tn.Onboarded || tn.Onboard(Setup{Template: "it"}, 1) == nil {
		t.Fatal("al terminar queda onboardeada y ya no se puede repetir")
	}
}

// Login, límite de usuarios y permisos por rol.
func TestUsersAndRoles(t *testing.T) {
	s, tn := newTenant(t)
	if _, err := s.Login("AGENTE@demo.test", "clave-de-prueba"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Login("agente@demo.test", "otra-clave"); err == nil {
		t.Fatal("contraseña incorrecta")
	}
	if s.AddUser(tn, "Otro", "agente@demo.test", "clave-de-prueba", 2, 1) == nil {
		t.Fatal("el email es único")
	}
	if s.AddUser(tn, "Otro", "otro@demo.test", "corta", 2, 1) == nil {
		t.Fatal("la contraseña necesita 8 caracteres")
	}
	for _, e := range []string{"a@demo.test", "b@demo.test", "c@demo.test"} {
		if err := s.AddUser(tn, "X", e, "clave-de-prueba", 2, 1); err != nil {
			t.Fatal(err)
		}
	}
	if s.AddUser(tn, "Sexto", "sexto@demo.test", "clave-de-prueba", 2, 1) == nil {
		t.Fatal("el titular puede crear hasta 5 usuarios")
	}

	// 3 es solicitante: crea tickets y ve solo los suyos; no resuelve ni configura.
	mine, err := tn.CreateIncident(vpn("mío"), 3)
	if err != nil {
		t.Fatal(err)
	}
	tn.CreateIncident(vpn("de otro"), 2)
	resuelto := "Resuelto"
	if tn.UpdateIncident(mine.ID, IncidentPatch{Status: &resuelto}, 3) == nil || s.AddUser(tn, "Y", "y@demo.test", "clave-de-prueba", 2, 3) == nil {
		t.Fatal("un solicitante no resuelve tickets ni crea usuarios")
	}
	v := tn.view(3)
	if len(v.Incidents) != 1 || len(v.Problems) != 0 || v.Users[0].Hash != "" {
		t.Fatalf("un solicitante ve solo sus incidentes y nunca hashes: %d incidentes", len(v.Incidents))
	}

	// Rol a medida: solo ver el panel.
	if err := tn.SaveRole(0, "Gerencia", []string{PermPanel, "inventado"}, 1); err != nil {
		t.Fatal(err)
	}
	gerencia := tn.Roles[len(tn.Roles)-1]
	if len(gerencia.Perms) != 1 || tn.SetUserRole(3, gerencia.ID, 1) != nil {
		t.Fatalf("permisos mal filtrados: %v", gerencia.Perms)
	}
	if _, err := tn.CreateIncident(vpn("z"), 3); err == nil {
		t.Fatal("con el rol Gerencia ya no puede crear tickets")
	}
	if len(tn.view(3).Incidents) != 2 {
		t.Fatal("quien ve el panel ve todos los incidentes")
	}
	if tn.DeleteRole(gerencia.ID, 1) == nil || tn.DeleteRole(1, 1) == nil || tn.SetUserRole(1, 2, 1) == nil {
		t.Fatal("no se borra un rol en uso ni el del titular, y el titular no cambia de rol")
	}
	if s.DeleteUser(tn, 1, 1) == nil || s.DeleteUser(tn, 3, 1) != nil || len(tn.Users) != 5 {
		t.Fatal("se puede eliminar un usuario, pero no al titular")
	}
}

// La demo tiene que arrancar con datos coherentes y una sugerencia por cuenta.
func TestSeed(t *testing.T) {
	for _, tn := range seed() {
		if len(tn.Suggestions()) == 0 {
			t.Errorf("%s: sin sugerencias de recurrencia", tn.Name)
		}
		if !checkPassword(tn.Users[0].Hash, demoPassword) || tn.Users[0].Email == "" {
			t.Errorf("%s: el titular no puede entrar con la contraseña de demo", tn.Name)
		}
		for _, inc := range tn.Incidents {
			if (inc.ResolvedAt != nil) != (inc.Status == tn.closedState()) {
				t.Errorf("%s #%d: estado %q no coincide con resolvedAt", tn.Name, inc.ID, inc.Status)
			}
		}
		for _, p := range tn.Problems {
			for _, inc := range tn.Incidents {
				if inc.ProblemID == p.ID && p.Status == Resuelto && inc.ResolvedAt == nil {
					t.Errorf("%s P-%d resuelto con #%d abierto", tn.Name, p.ID, inc.ID)
				}
			}
		}
	}
}
