package service_test

import (
	"testing"

	"taas-backend/internal/domain"
	"taas-backend/internal/repository"
	"taas-backend/internal/service"
)

const (
	tid   = 1 // la única cuenta de cada test
	owner = 1
	agent = 2
	guest = 3 // solicitante
	pass  = "clave-de-prueba"
)

// newAccount: una cuenta recién contratada y onboardeada (IT, todo activado) con titular, agente y solicitante.
func newAccount(t *testing.T) (*service.Services, *domain.Tenant) {
	t.Helper()
	tenants, _ := repository.NewJSONTenants("") // solo en memoria
	sv := service.New(tenants, repository.NewMemorySessions())
	if _, err := sv.Auth.Signup("Demo", "Titular", "titular@demo.test", pass); err != nil {
		t.Fatal(err)
	}
	setup := service.Full
	setup.Template = "it"
	must(t, sv.Setup.Onboard(tid, owner, setup))
	must(t, sv.Setup.Finish(tid, owner))
	must(t, sv.Accounts.AddUser(tid, owner, "Agente", "agente@demo.test", pass, 2))
	must(t, sv.Accounts.AddUser(tid, owner, "Solicitante", "solicitante@demo.test", pass, 3))
	tn, _ := tenants.Get(tid)
	return sv, tn
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func vpn(title string) domain.NewIncident {
	return domain.NewIncident{Title: title, Service: "VPN", Priority: "alta", Fields: map[string]string{"sede": "Remoto"}}
}

// Flujo completo: incidentes recurrentes -> problema -> error conocido -> solución que cierra los incidentes.
func TestProblemLifecycle(t *testing.T) {
	sv, tn := newAccount(t)
	a, err := sv.Incidents.Create(tid, agent, vpn("no anda"))
	must(t, err)
	b, _ := sv.Incidents.Create(tid, agent, vpn("no anda de nuevo"))
	if a.Status != "Abierto" || a.DueAt.Sub(a.CreatedAt).Hours() != 4 {
		t.Fatalf("estado inicial o SLA mal: %s %v", a.Status, a.DueAt.Sub(a.CreatedAt))
	}

	if sg := service.Suggestions(tn); len(sg) != 1 || len(sg[0].IncidentIDs) != 2 {
		t.Fatalf("esperaba 1 sugerencia con 2 incidentes, obtuve %+v", sg)
	}
	p, err := sv.Problems.Create(tid, agent, domain.NewProblem{Title: "VPN inestable", Service: "VPN", IncidentIDs: []int{a.ID, b.ID}})
	must(t, err)
	if len(service.Suggestions(tn)) != 0 {
		t.Fatal("los incidentes ya vinculados no deben sugerirse")
	}

	if err := sv.Problems.Advance(tid, agent, p.ID, domain.Advance{}); err != nil || p.Status != domain.EnAnalisis {
		t.Fatalf("status=%s err=%v", p.Status, err)
	}
	if sv.Problems.Advance(tid, agent, p.ID, domain.Advance{}) == nil {
		t.Fatal("no debe ser error conocido sin causa raíz")
	}
	if err := sv.Problems.Advance(tid, agent, p.ID, domain.Advance{RootCause: "licencias", Workaround: "vpn2"}); err != nil || p.Status != domain.ErrorConocido {
		t.Fatalf("status=%s err=%v", p.Status, err)
	}
	if sv.Problems.Advance(tid, agent, p.ID, domain.Advance{}) == nil {
		t.Fatal("no debe resolverse sin solución")
	}
	if err := sv.Problems.Advance(tid, agent, p.ID, domain.Advance{Solution: "más licencias"}); err != nil || p.Status != domain.Resuelto {
		t.Fatalf("status=%s err=%v", p.Status, err)
	}
	if a.ResolvedAt == nil || b.Status != "Resuelto" {
		t.Fatal("resolver el problema debe cerrar sus incidentes")
	}
	if sv.Incidents.Update(tid, agent, a.ID, domain.IncidentPatch{ProblemID: &p.ID}) == nil || sv.Incidents.Update(tid, agent, 99, domain.IncidentPatch{}) == nil {
		t.Fatal("no se puede vincular a un problema resuelto ni tocar un incidente inexistente")
	}
}

// La configuración es de cada cuenta: valida campos propios y migra estados renombrados o eliminados.
func TestConfig(t *testing.T) {
	sv, _ := newAccount(t)
	if _, err := sv.Incidents.Create(tid, agent, domain.NewIncident{Title: "x", Service: "VPN", Priority: "alta"}); err == nil {
		t.Fatal("Sede es obligatoria")
	}
	if _, err := sv.Incidents.Create(tid, agent, domain.NewIncident{Title: "x", Service: "Tomógrafo", Priority: "alta"}); err == nil {
		t.Fatal("el servicio tiene que ser del catálogo")
	}
	inc, _ := sv.Incidents.Create(tid, agent, vpn("x"))
	enCurso := "En curso"
	must(t, sv.Incidents.Update(tid, agent, inc.ID, domain.IncidentPatch{Status: &enCurso}))

	_, c, _ := service.BuildConfig("it", service.Full)
	c.States[1] = "Trabajando"
	if sv.Setup.SetConfig(tid, agent, c) == nil {
		t.Fatal("un agente no puede cambiar la configuración")
	}
	if err := sv.Setup.SetConfig(tid, owner, c); err != nil || inc.Status != "Trabajando" {
		t.Fatalf("renombrar un estado debe arrastrar sus incidentes: %q %v", inc.Status, err)
	}
	_, c, _ = service.BuildConfig("it", service.Full)
	c.States = []string{"Nuevo", "Cerrado"}
	c.SLAHours["alta"] = 2
	must(t, sv.Setup.SetConfig(tid, owner, c))
	if inc.Status != "Nuevo" {
		t.Fatalf("un estado eliminado debe volver al inicial, quedó %q", inc.Status)
	}
	next, _ := sv.Incidents.Create(tid, agent, vpn("y"))
	if next.DueAt.Sub(next.CreatedAt).Hours() != 2 {
		t.Fatal("el nuevo SLA debe aplicarse a los incidentes nuevos")
	}
	c.States = []string{"Único"}
	if sv.Setup.SetConfig(tid, owner, c) == nil {
		t.Fatal("hacen falta al menos dos estados")
	}
}

// Las respuestas del onboarding definen la configuración inicial de la cuenta.
func TestOnboarding(t *testing.T) {
	tenants, _ := repository.NewJSONTenants("")
	sv := service.New(tenants, repository.NewMemorySessions())
	sv.Auth.Signup("Chica", "Titular", "chica@demo.test", pass)
	tn, _ := tenants.Get(tid)
	if tn.Onboarded || sv.Setup.Finish(tid, owner) == nil {
		t.Fatal("una cuenta nueva arranca sin onboarding y no se puede terminar sin configurar")
	}
	if sv.Setup.Onboard(tid, owner, domain.Setup{Template: "otro"}) == nil || sv.Setup.Onboard(tid, owner, domain.Setup{Template: "nada"}) == nil {
		t.Fatal("el rubro 'otro' exige describirlo, y el rubro tiene que existir")
	}

	// Trabaja solo, sin plazos ni recurrencias: lo mínimo.
	must(t, sv.Setup.Onboard(tid, owner, domain.Setup{Template: "salud"}))
	c := tn.Config
	if len(c.States) != 2 || len(c.Fields) != 0 || c.Modules.SLA || c.Modules.Problems || tn.Industry != "Salud" {
		t.Fatalf("sin equipo, plazos ni datos propios no debe haber flujo, SLA, campos ni problemas: %+v", c)
	}

	// Vuelve atrás y cambia las respuestas: rubro propio, servicios ajustados, con plazos y equipo.
	must(t, sv.Setup.Onboard(tid, owner, domain.Setup{Template: "otro", Industry: " Estudio contable ",
		Services: []string{"Sueldos", " Impuestos ", ""}, Team: true, SLA: true}))
	c = tn.Config
	if tn.Industry != "Estudio contable" || len(c.Services) != 2 || c.Services[1] != "Impuestos" || len(c.States) != 3 || !c.Modules.SLA {
		t.Fatalf("no se aplicaron las respuestas: %q %+v", tn.Industry, c)
	}
	if sv.Setup.Onboard(tid, owner, domain.Setup{Template: "it", Services: []string{"", " "}}) == nil || tn.Industry != "Estudio contable" {
		t.Fatal("sin ningún servicio no hay ticketera: debe fallar sin tocar lo ya configurado")
	}

	must(t, sv.Setup.Onboard(tid, owner, domain.Setup{Template: "salud"}))
	inc, err := sv.Incidents.Create(tid, owner, domain.NewIncident{Title: "primer ticket", Service: tn.Config.Services[0], Priority: "alta"})
	if err != nil || inc.DueAt != nil {
		t.Fatalf("el primer ticket se crea durante el onboarding; sin SLA no hay vencimiento: %v %v", inc, err)
	}
	if _, err := sv.Problems.Create(tid, owner, domain.NewProblem{Title: "p", Service: tn.Config.Services[0]}); err == nil {
		t.Fatal("sin el módulo no se pueden crear problemas")
	}
	if sv.Setup.Finish(tid, owner) != nil || !tn.Onboarded || sv.Setup.Onboard(tid, owner, domain.Setup{Template: "it"}) == nil {
		t.Fatal("al terminar queda onboardeada y ya no se puede repetir")
	}
}

// Login, sesiones, límite de usuarios y permisos por rol.
func TestUsersAndRoles(t *testing.T) {
	sv, tn := newAccount(t)
	token, err := sv.Auth.Login("AGENTE@demo.test", pass)
	must(t, err)
	if gotT, gotU, err := sv.Auth.Authenticate(token); err != nil || gotT != tid || gotU != agent {
		t.Fatalf("el token no identifica al agente: %d %d %v", gotT, gotU, err)
	}
	if _, err := sv.Auth.Login("agente@demo.test", "otra-clave"); err == nil {
		t.Fatal("contraseña incorrecta")
	}
	if sv.Accounts.AddUser(tid, owner, "Otro", "agente@demo.test", pass, 2) == nil {
		t.Fatal("el email es único")
	}
	if sv.Accounts.AddUser(tid, owner, "Otro", "otro@demo.test", "corta", 2) == nil {
		t.Fatal("la contraseña necesita 8 caracteres")
	}
	for _, e := range []string{"a@demo.test", "b@demo.test", "c@demo.test"} {
		must(t, sv.Accounts.AddUser(tid, owner, "X", e, pass, 2))
	}
	if sv.Accounts.AddUser(tid, owner, "Sexto", "sexto@demo.test", pass, 2) == nil {
		t.Fatal("el titular puede crear hasta 5 usuarios")
	}

	// El solicitante crea tickets y ve solo los suyos; no resuelve ni configura.
	mine, err := sv.Incidents.Create(tid, guest, vpn("mío"))
	must(t, err)
	sv.Incidents.Create(tid, agent, vpn("de otro"))
	resuelto := "Resuelto"
	if sv.Incidents.Update(tid, guest, mine.ID, domain.IncidentPatch{Status: &resuelto}) == nil ||
		sv.Accounts.AddUser(tid, guest, "Y", "y@demo.test", pass, 2) == nil {
		t.Fatal("un solicitante no resuelve tickets ni crea usuarios")
	}
	v, _ := sv.State.View(tid, guest)
	if len(v.Incidents) != 1 || len(v.Problems) != 0 || v.Users[0].Hash != "" {
		t.Fatalf("un solicitante ve solo sus incidentes y nunca hashes: %d incidentes", len(v.Incidents))
	}

	// Rol a medida: solo ver el panel.
	must(t, sv.Accounts.SaveRole(tid, owner, 0, "Gerencia", []string{domain.PermPanel, "inventado"}))
	gerencia := tn.Roles[len(tn.Roles)-1]
	if len(gerencia.Perms) != 1 || sv.Accounts.SetUserRole(tid, owner, guest, gerencia.ID) != nil {
		t.Fatalf("permisos mal filtrados: %v", gerencia.Perms)
	}
	if _, err := sv.Incidents.Create(tid, guest, vpn("z")); err == nil {
		t.Fatal("con el rol Gerencia ya no puede crear tickets")
	}
	if v, _ := sv.State.View(tid, guest); len(v.Incidents) != 2 {
		t.Fatal("quien ve el panel ve todos los incidentes")
	}
	if sv.Accounts.DeleteRole(tid, owner, gerencia.ID) == nil || sv.Accounts.DeleteRole(tid, owner, 1) == nil || sv.Accounts.SetUserRole(tid, owner, owner, 2) == nil {
		t.Fatal("no se borra un rol en uso ni el del titular, y el titular no cambia de rol")
	}

	// Eliminar un usuario también cierra su sesión.
	if sv.Accounts.DeleteUser(tid, owner, owner) == nil || sv.Accounts.DeleteUser(tid, owner, agent) != nil || len(tn.Users) != 5 {
		t.Fatal("se puede eliminar un usuario, pero no al titular")
	}
	if _, _, err := sv.Auth.Authenticate(token); err == nil {
		t.Fatal("el usuario eliminado no puede seguir usando su sesión")
	}
}
