package service_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

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
	if a.Status != "Nuevo" || a.DueAt.Sub(a.CreatedAt).Hours() != 4 {
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
	c, _ := sv.Incidents.Create(tid, agent, vpn("otro más"))
	if sv.Incidents.Update(tid, agent, c.ID, domain.IncidentPatch{ProblemID: &p.ID}) == nil || sv.Incidents.Update(tid, agent, 99, domain.IncidentPatch{}) == nil {
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
	if len(c.States) != 2 || len(c.PauseStates) != 0 || len(c.Fields) != 0 || c.Modules.SLA || c.Modules.Problems || tn.Industry != "Salud" {
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

// Un pedido que falla a mitad de camino no deja la cuenta modificada.
func TestFailedUpdateLeavesAccountUntouched(t *testing.T) {
	sv, tn := newAccount(t)
	p, err := sv.Problems.Create(tid, agent, domain.NewProblem{Title: "p", Service: "VPN"})
	must(t, err)
	must(t, sv.Problems.Advance(tid, agent, p.ID, domain.Advance{}))
	must(t, sv.Problems.Advance(tid, agent, p.ID, domain.Advance{RootCause: "x"}))
	must(t, sv.Problems.Advance(tid, agent, p.ID, domain.Advance{Solution: "y"}))

	before := len(tn.Incidents)
	in := vpn("vinculado a un problema resuelto")
	in.ProblemID = p.ID
	if _, err := sv.Incidents.Create(tid, agent, in); err == nil || len(tn.Incidents) != before {
		t.Fatalf("el alta fallida no debe dejar el incidente creado: err=%v, %d -> %d", err, before, len(tn.Incidents))
	}

	b, _ := sv.Incidents.Create(tid, agent, vpn("b"))
	who, bad := agent, "No existe"
	if sv.Incidents.Update(tid, agent, b.ID, domain.IncidentPatch{AssigneeID: &who, Status: &bad}) == nil || b.AssigneeID != 0 {
		t.Fatalf("con un estado inválido no debe aplicarse el resto del pedido: asignado=%d", b.AssigneeID)
	}
}

// Un incidente se puede desvincular de su problema, y queda en el historial.
func TestUnlinkIncident(t *testing.T) {
	sv, _ := newAccount(t)
	a, _ := sv.Incidents.Create(tid, agent, vpn("a"))
	p, err := sv.Problems.Create(tid, agent, domain.NewProblem{Title: "p", Service: "VPN", IncidentIDs: []int{a.ID, a.ID}})
	must(t, err)
	if len(p.History) != 1 || p.History[0].Text != "Problema creado desde 1 incidentes recurrentes" {
		t.Fatalf("los ids repetidos cuentan una sola vez: %+v", p.History)
	}
	none := 0
	must(t, sv.Incidents.Update(tid, agent, a.ID, domain.IncidentPatch{ProblemID: &none}))
	if a.ProblemID != 0 || p.History[len(p.History)-1].Text != "#1 desvinculado del problema" {
		t.Fatalf("no se desvinculó: problemId=%d historial=%+v", a.ProblemID, p.History)
	}
	if e := last(a); e.Kind != domain.EvProblem || e.Text != "Desvinculado del problema P-1" {
		t.Fatalf("el incidente también lo registra: %+v", a.History)
	}
}

// Reordenar servicios o estados no los renombra: cada incidente conserva el suyo.
func TestReorderIsNotRename(t *testing.T) {
	sv, tn := newAccount(t)
	inc, _ := sv.Incidents.Create(tid, agent, vpn("x"))
	c := tn.Config
	c.Services = append([]string{}, c.Services...)
	c.Services[0], c.Services[1] = c.Services[1], c.Services[0] // VPN <-> Correo
	must(t, sv.Setup.SetConfig(tid, owner, c))
	if inc.Service != "VPN" {
		t.Fatalf("reordenar el catálogo cambió el servicio del incidente a %q", inc.Service)
	}
}

// Al eliminar a un usuario, sus problemas pasan al titular.
func TestDeleteUserReassignsProblems(t *testing.T) {
	sv, _ := newAccount(t)
	p, err := sv.Problems.Create(tid, agent, domain.NewProblem{Title: "p", Service: "VPN"})
	must(t, err)
	must(t, sv.Accounts.DeleteUser(tid, owner, agent))
	if p.OwnerID != owner {
		t.Fatalf("el problema quedó a cargo de un usuario eliminado: %d", p.OwnerID)
	}
}

// Quien gestiona problemas ve los incidentes a los que apuntan las sugerencias.
func TestProblemManagerSeesIncidents(t *testing.T) {
	sv, tn := newAccount(t)
	sv.Incidents.Create(tid, agent, vpn("a"))
	sv.Incidents.Create(tid, agent, vpn("b"))
	must(t, sv.Accounts.SaveRole(tid, owner, 0, "Analista", []string{domain.PermProblems}))
	must(t, sv.Accounts.SetUserRole(tid, owner, guest, tn.Roles[len(tn.Roles)-1].ID))
	v, _ := sv.State.View(tid, guest)
	if len(v.Suggestions) != 1 || len(v.Incidents) != 2 {
		t.Fatalf("sugerencias=%d incidentes visibles=%d", len(v.Suggestions), len(v.Incidents))
	}
}

// Un email inexistente falla igual que una contraseña incorrecta.
func TestLoginUnknownEmail(t *testing.T) {
	sv, _ := newAccount(t)
	_, errUnknown := sv.Auth.Login("nadie@demo.test", pass)
	_, errWrong := sv.Auth.Login("agente@demo.test", "otra-clave")
	if errUnknown == nil || errWrong == nil || errUnknown.Error() != errWrong.Error() {
		t.Fatalf("los errores tienen que ser iguales: %v / %v", errUnknown, errWrong)
	}
}

// Reenviar el problema que el incidente ya tiene no lo revalida: el resto del cambio se aplica.
func TestResendLinkedProblem(t *testing.T) {
	sv, _ := newAccount(t)
	a, _ := sv.Incidents.Create(tid, agent, vpn("a"))
	p, err := sv.Problems.Create(tid, agent, domain.NewProblem{Title: "p", Service: "VPN", IncidentIDs: []int{a.ID}})
	must(t, err)
	must(t, sv.Problems.Advance(tid, agent, p.ID, domain.Advance{}))
	must(t, sv.Problems.Advance(tid, agent, p.ID, domain.Advance{RootCause: "x"}))
	must(t, sv.Problems.Advance(tid, agent, p.ID, domain.Advance{Solution: "y"}))

	who := agent
	if err := sv.Incidents.Update(tid, agent, a.ID, domain.IncidentPatch{AssigneeID: &who, ProblemID: &p.ID}); err != nil || a.AssigneeID != agent {
		t.Fatalf("el problema no cambió, no debe rechazarse por estar resuelto: asignado=%d %v", a.AssigneeID, err)
	}
}

// Si no se puede guardar, la cuenta queda como estaba: reintentar no duplica nada.
func TestFailedSaveLeavesAccountUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	tenants, err := repository.NewJSONTenants(path)
	must(t, err)
	sv := service.New(tenants, repository.NewMemorySessions())
	_, err = sv.Auth.Signup("Demo", "Titular", "titular@demo.test", pass)
	must(t, err)

	must(t, os.Mkdir(path+".tmp", 0o700)) // hace fallar la escritura
	inc := domain.NewIncident{Title: "x", Service: "Sistemas", Priority: "alta"}
	if _, err := sv.Incidents.Create(tid, owner, inc); !errors.Is(err, domain.ErrInternal) {
		t.Fatalf("esperaba un error interno, obtuve %v", err)
	}
	if tn, _ := tenants.Get(tid); len(tn.Incidents) != 0 {
		t.Fatal("el incidente que no se pudo guardar no debe quedar en memoria")
	}
	if _, err := sv.Auth.Signup("Otra", "Titular", "otra@demo.test", pass); !errors.Is(err, domain.ErrInternal) {
		t.Fatalf("esperaba un error interno, obtuve %v", err)
	}

	must(t, os.Remove(path+".tmp"))
	_, err = sv.Incidents.Create(tid, owner, inc)
	must(t, err)
	_, err = sv.Auth.Signup("Otra", "Titular", "otra@demo.test", pass)
	must(t, err)
	if tn, _ := tenants.Get(tid); len(tn.Incidents) != 1 {
		t.Fatalf("el reintento debe crear un solo incidente, hay %d", len(tn.Incidents))
	}
}

// Los casos de uso se pueden llamar en paralelo.
func TestConcurrentUse(t *testing.T) {
	sv, _ := newAccount(t)
	token, err := sv.Auth.Login("agente@demo.test", pass)
	must(t, err)
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := sv.Auth.Authenticate(token); err != nil {
				t.Error(err)
			}
			if _, err := sv.Incidents.Create(tid, agent, vpn(fmt.Sprint(i))); err != nil {
				t.Error(err)
			}
			if _, err := sv.State.View(tid, owner); err != nil {
				t.Error(err)
			}
			sv.Auth.Logout("token-que-no-existe")
		}()
	}
	wg.Wait()
	if v, _ := sv.State.View(tid, owner); len(v.Incidents) != 10 {
		t.Fatalf("esperaba 10 incidentes, hay %d", len(v.Incidents))
	}
}

// --- Ciclo de vida del incidente ---

var (
	nuevo    = "Nuevo"
	enCurso  = "En curso"
	resuelto = "Resuelto"
)

// resolve es el pedido que resuelve un incidente con código y nota.
func resolve(code, note string) domain.IncidentPatch {
	return domain.IncidentPatch{Status: &resuelto, ResolutionCode: code, ResolutionNote: note}
}

func last(inc *domain.Incident) domain.Event { return inc.History[len(inc.History)-1] }

// idle hace como si el incidente no tuviera actividad desde hace d: lleva a ese momento la resolución y
// todos los eventos posteriores (el historial sigue en orden).
func idle(inc *domain.Incident, d time.Duration) {
	cut := time.Now().Add(-d)
	if inc.ResolvedAt != nil && inc.ResolvedAt.After(cut) {
		inc.ResolvedAt = &cut
	}
	for i := range inc.History {
		if inc.History[i].At.After(cut) {
			inc.History[i].At = cut
		}
	}
}

const day = 24 * time.Hour

// Registro, asignación y vinculación quedan en el historial del incidente.
func TestIncidentHistory(t *testing.T) {
	sv, _ := newAccount(t)
	p, err := sv.Problems.Create(tid, agent, domain.NewProblem{Title: "p", Service: "VPN"})
	must(t, err)
	in := vpn("x")
	in.AssigneeID, in.ProblemID = agent, p.ID
	inc, err := sv.Incidents.Create(tid, agent, in)
	must(t, err)
	got := []string{}
	for _, e := range inc.History {
		got = append(got, e.Kind+": "+e.Text)
	}
	want := fmt.Sprint([]string{"creado: Incidente registrado", "asignado: Asignado a Agente", "problema: Vinculado al problema P-1"})
	if fmt.Sprint(got) != want || !inc.History[0].At.Equal(inc.CreatedAt) {
		t.Fatalf("historial del registro: %v", got)
	}
	none := 0
	must(t, sv.Incidents.Update(tid, agent, inc.ID, domain.IncidentPatch{AssigneeID: &none, Status: &enCurso}))
	if n := len(inc.History); inc.History[n-2].Text != "Sin asignar" || inc.History[n-1].Text != "De Nuevo a En curso" {
		t.Fatalf("faltan el cambio de asignado o de estado: %+v", inc.History)
	}
}

// Resolver exige código y nota y deja un solo evento; si falta algo no se aplica nada del pedido.
// Un resuelto no cambia de estado.
func TestResolveRequiresCodeAndNote(t *testing.T) {
	sv, tn := newAccount(t)
	inc, _ := sv.Incidents.Create(tid, agent, vpn("x"))
	if got := inc.ResponseDueAt.Sub(inc.CreatedAt); got != time.Duration(tn.Config.ResponseHours["alta"])*time.Hour {
		t.Fatalf("vencimiento de la primera respuesta: %v", got)
	}
	who, before := agent, len(inc.History)
	for _, p := range []domain.IncidentPatch{resolve("", "se reinició"), resolve("solucionado", "  "), resolve("inventado", "se reinició")} {
		p.AssigneeID = &who
		if sv.Incidents.Update(tid, agent, inc.ID, p) == nil {
			t.Fatalf("debe fallar sin código válido o sin nota: %+v", p)
		}
	}
	if inc.Status != nuevo || inc.ResolvedAt != nil || inc.AssigneeID != 0 || len(inc.History) != before {
		t.Fatalf("el pedido fallido modificó el incidente: %+v", inc)
	}

	must(t, sv.Incidents.Update(tid, agent, inc.ID, resolve("solucionado", " se reinició ")))
	if inc.ResolvedAt == nil || inc.ClosedAt != nil || inc.ResolutionCode != "solucionado" || inc.ResolutionNote != "se reinició" ||
		len(inc.History) != before+1 || last(inc).Kind != domain.EvResolved || last(inc).Text != "De Nuevo a Resuelto · Solucionado: se reinició" {
		t.Fatalf("resolución mal registrada (un solo evento, sin cerrar): %+v", inc)
	}
	err := sv.Incidents.Update(tid, agent, inc.ID, domain.IncidentPatch{Status: &enCurso})
	if err == nil || err.Error() != "el incidente está resuelto: reabrilo para seguir trabajando" || inc.Status != resuelto {
		t.Fatalf("un resuelto no cambia de estado: %v", err)
	}
	if sv.Incidents.Update(tid, agent, inc.ID, domain.IncidentPatch{Status: &resuelto}) != nil {
		t.Fatal("reenviar el estado que ya tiene no es un cambio")
	}
}

// Duplicado y cancelado resuelven y cierran en el mismo momento; el resto queda resuelto.
func TestClosingCodes(t *testing.T) {
	sv, _ := newAccount(t)
	for _, code := range domain.ResolutionCodes {
		inc, _ := sv.Incidents.Create(tid, agent, vpn(code))
		must(t, sv.Incidents.Update(tid, agent, inc.ID, resolve(code, "nota")))
		if e := inc.History[1]; e.Kind != domain.EvResolved || e.Text != "De Nuevo a Resuelto · "+domain.ResolutionLabels[code]+": nota" || inc.ResolutionCode != code {
			t.Fatalf("%s: el evento lleva la etiqueta y resolutionCode el código: %q %q", code, e.Text, inc.ResolutionCode)
		}
		closing := code == "duplicado" || code == "cancelado"
		if closing != inc.Closed() || (closing && !inc.ClosedAt.Equal(*inc.ResolvedAt)) {
			t.Fatalf("%s: closedAt=%v resolvedAt=%v", code, inc.ClosedAt, inc.ResolvedAt)
		}
		if n := len(inc.History); closing && (inc.History[n-2].Kind != domain.EvResolved || last(inc).Kind != domain.EvClosed || last(inc).Text != "Cerrado por Agente") {
			t.Fatalf("%s: esperaba los eventos resuelto y cerrado: %+v", code, inc.History)
		}
	}
}

// Reabrir: solo quien resuelve, con motivo, solo un resuelto (no un abierto ni un cerrado). Un solo evento y libera la retención.
func TestReopen(t *testing.T) {
	sv, _ := newAccount(t)
	mine, _ := sv.Incidents.Create(tid, guest, vpn("mío"))
	due := *mine.DueAt
	if err := sv.Incidents.Reopen(tid, agent, mine.ID, "sigue"); err == nil || errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("un incidente abierto no se reabre (400): %v", err)
	}
	must(t, sv.Incidents.Update(tid, agent, mine.ID, resolve("solucionado", "ok")))
	if err := sv.Incidents.Reopen(tid, guest, mine.ID, "volvió a pasar"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("quien solo reporta no reabre: %v", err)
	}
	if sv.Incidents.Reopen(tid, agent, mine.ID, "  ") == nil {
		t.Fatal("el motivo es obligatorio")
	}
	must(t, sv.Incidents.Comment(tid, guest, mine.ID, "sigue sin andar", true))
	before := len(mine.History)
	must(t, sv.Incidents.Reopen(tid, agent, mine.ID, "volvió a pasar"))
	if mine.Status != nuevo || mine.ResolvedAt != nil || mine.ResolutionCode != "" || mine.ResolutionNote != "" ||
		mine.ReopenCount != 1 || mine.AutoCloseHold || !mine.DueAt.Equal(due) || len(mine.History) != before+1 ||
		last(mine).Kind != domain.EvReopened || last(mine).Text != "De Resuelto a Nuevo: volvió a pasar" {
		t.Fatalf("reapertura mal aplicada: %+v", mine)
	}

	must(t, sv.Incidents.Update(tid, agent, mine.ID, resolve("solucionado", "ok")))
	must(t, sv.Incidents.Close(tid, agent, mine.ID))
	if err := sv.Incidents.Reopen(tid, agent, mine.ID, "x"); err != domain.ErrClosed || mine.ResolvedAt == nil {
		t.Fatalf("un cerrado no se reabre: %v", err)
	}
}

// Cerrar: solo quien resuelve y solo un resuelto.
func TestClose(t *testing.T) {
	sv, _ := newAccount(t)
	inc, _ := sv.Incidents.Create(tid, guest, vpn("x"))
	if err := sv.Incidents.Close(tid, agent, inc.ID); err == nil || errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("un abierto no se cierra (400): %v", err)
	}
	must(t, sv.Incidents.Update(tid, agent, inc.ID, resolve("solucionado", "ok")))
	if err := sv.Incidents.Close(tid, guest, inc.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("quien solo reporta no cierra: %v", err)
	}
	must(t, sv.Incidents.Close(tid, agent, inc.ID))
	if e := last(inc); inc.ClosedAt == nil || !inc.ClosedAt.Equal(e.At) || e.Kind != domain.EvClosed || e.Text != "Cerrado por Agente" || e.UserID != agent {
		t.Fatalf("cierre mal registrado: %+v", inc)
	}
	if err := sv.Incidents.Close(tid, agent, inc.ID); err != domain.ErrClosed {
		t.Fatalf("un cerrado no se vuelve a cerrar: %v", err)
	}
}

// Un cerrado es de solo lectura: ningún pedido sobre el incidente lo modifica. Crear un problema sí lo vincula.
func TestClosedIsReadOnly(t *testing.T) {
	sv, _ := newAccount(t)
	inc, _ := sv.Incidents.Create(tid, guest, vpn("x"))
	must(t, sv.Incidents.Update(tid, agent, inc.ID, resolve("duplicado", "es otro")))
	p, err := sv.Problems.Create(tid, agent, domain.NewProblem{Title: "p", Service: "VPN"})
	must(t, err)
	before := fmt.Sprintf("%+v", *inc)
	who := agent
	for name, err := range map[string]error{
		"asignado":  sv.Incidents.Update(tid, agent, inc.ID, domain.IncidentPatch{AssigneeID: &who}),
		"estado":    sv.Incidents.Update(tid, agent, inc.ID, domain.IncidentPatch{Status: &nuevo}),
		"problema":  sv.Incidents.Update(tid, agent, inc.ID, domain.IncidentPatch{ProblemID: &p.ID}),
		"respuesta": sv.Incidents.Comment(tid, guest, inc.ID, "hola", true),
		"nota":      sv.Incidents.Comment(tid, agent, inc.ID, "hola", false),
		"reabrir":   sv.Incidents.Reopen(tid, agent, inc.ID, "x"),
		"cerrar":    sv.Incidents.Close(tid, agent, inc.ID),
	} {
		if err != domain.ErrClosed {
			t.Errorf("%s sobre un cerrado: esperaba %q, obtuve %v", name, domain.ErrClosed, err)
		}
	}
	if after := fmt.Sprintf("%+v", *inc); after != before {
		t.Fatalf("el cerrado cambió:\n%s\n%s", before, after)
	}

	// La excepción: crear un problema lo vincula (sigue cerrado), y resolver el problema no lo toca.
	q, err := sv.Problems.Create(tid, agent, domain.NewProblem{Title: "q", Service: "VPN", IncidentIDs: []int{inc.ID}})
	must(t, err)
	if inc.ProblemID != q.ID || !inc.Closed() || last(inc).Kind != domain.EvProblem || q.History[0].Text != "Problema creado desde 1 incidentes recurrentes" {
		t.Fatalf("crear el problema vincula el cerrado: %+v / %+v", inc, q.History)
	}
	before = fmt.Sprintf("%+v", *inc)
	must(t, sv.Problems.Advance(tid, agent, q.ID, domain.Advance{}))
	must(t, sv.Problems.Advance(tid, agent, q.ID, domain.Advance{RootCause: "x"}))
	must(t, sv.Problems.Advance(tid, agent, q.ID, domain.Advance{Solution: "y"}))
	if after := fmt.Sprintf("%+v", *inc); after != before || q.History[len(q.History)-1].Text != "Resuelto · 0 incidentes resueltos" {
		t.Fatalf("resolver el problema no toca sus cerrados:\n%s\n%s", before, after)
	}
}

// El cierre automático: tras autoCloseDays sin actividad (el default es 2 en todas las plantillas), con la fecha
// que correspondía y el evento sin usuario. Cada evento reinicia la cuenta, el retenido no se cierra y se usa el
// valor vigente de autoCloseDays.
func TestAutoClose(t *testing.T) {
	sv, tn := newAccount(t)
	for _, tpl := range sv.Setup.Templates() {
		if tpl.Config.AutoCloseDays != 2 {
			t.Fatalf("plantilla %s: autoCloseDays=%d", tpl.Key, tpl.Config.AutoCloseDays)
		}
	}
	incs := make([]*domain.Incident, 4)
	for i := range incs {
		incs[i], _ = sv.Incidents.Create(tid, guest, vpn(fmt.Sprint(i)))
		must(t, sv.Incidents.Update(tid, agent, incs[i].ID, resolve("solucionado", "ok")))
	}
	old, held, active, recent := incs[0], incs[1], incs[2], incs[3]
	must(t, sv.Incidents.Comment(tid, guest, held.ID, "sigue fallando", true))
	must(t, sv.Incidents.Comment(tid, agent, active.ID, "lo dejo en observación", false))
	idle(old, 3*day)
	idle(held, 5*day)
	idle(recent, day)
	// Resuelto hace 3 días, pero con una nota de hace 1: la nota reinició la cuenta.
	idle(active, 3*day)
	note := time.Now().Add(-day)
	active.History[len(active.History)-1].At = note

	if _, err := sv.State.View(tid, owner); err != nil {
		t.Fatal(err)
	}
	if e := last(old); !old.Closed() || !old.ClosedAt.Equal(old.History[len(old.History)-2].At.Add(2*day)) || e.Kind != domain.EvClosed ||
		e.UserID != 0 || e.Text != "Cerrado automáticamente tras 2 días sin actividad" || !e.At.Equal(*old.ClosedAt) {
		t.Fatalf("cierre automático mal registrado: %+v", old)
	}
	if held.Closed() || recent.Closed() || active.Closed() {
		t.Fatal("no se cierran los retenidos por quien reportó ni los que tuvieron actividad hace menos de autoCloseDays")
	}

	// Sin actividad nueva, la cuenta corre desde la nota.
	note = time.Now().Add(-2*day - time.Hour)
	active.History[len(active.History)-1].At = note
	if _, err := sv.State.View(tid, owner); err != nil || !active.Closed() || !active.ClosedAt.Equal(note.Add(2*day)) {
		t.Fatalf("el cierre se cuenta desde el último evento: %v %v", active.ClosedAt, err)
	}

	// Con un autoCloseDays menor, el próximo pedido ya cierra el reciente; cualquier pedido barre, no solo la vista.
	c := tn.Config
	c.AutoCloseDays = 1
	must(t, sv.Setup.SetConfig(tid, owner, c))
	if _, err := sv.Incidents.Create(tid, guest, vpn("barre")); err != nil {
		t.Fatal(err)
	}
	if !recent.Closed() || !recent.ClosedAt.Equal(recent.History[len(recent.History)-2].At.Add(day)) || held.Closed() {
		t.Fatalf("barrido con el valor nuevo: reciente=%v retenido=%v", recent.ClosedAt, held.ClosedAt)
	}
	c.AutoCloseDays = 0
	if sv.Setup.SetConfig(tid, owner, c) == nil {
		t.Fatal("autoCloseDays tiene que ser de al menos 1 día")
	}
}

// El barrido se guarda, aunque el pedido que lo disparó falle.
func TestAutoClosePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	tenants, err := repository.NewJSONTenants(path)
	must(t, err)
	sv := service.New(tenants, repository.NewMemorySessions())
	_, err = sv.Auth.Signup("Demo", "Titular", "titular@demo.test", pass)
	must(t, err)
	tn, _ := tenants.Get(tid)
	for range 2 {
		inc, err := sv.Incidents.Create(tid, owner, domain.NewIncident{Title: "x", Service: "Sistemas", Priority: "alta"})
		must(t, err)
		must(t, sv.Incidents.Update(tid, owner, inc.ID, domain.IncidentPatch{Status: &resuelto, ResolutionCode: "solucionado", ResolutionNote: "ok"}))
	}
	saved := func(id int) bool {
		var data []domain.Tenant
		raw, _ := os.ReadFile(path)
		must(t, json.Unmarshal(raw, &data))
		return data[0].Incidents[id-1].ClosedAt != nil
	}

	idle(tn.Incidents[0], 3*day)
	if _, err := sv.State.View(tid, owner); err != nil || !saved(1) {
		t.Fatalf("el barrido de la vista no se guardó: %v", err)
	}
	idle(tn.Incidents[1], 3*day)
	bad := "No existe"
	if sv.Incidents.Update(tid, owner, 1, domain.IncidentPatch{Status: &bad}) == nil || !saved(2) {
		t.Fatal("el barrido de un pedido fallido no se guardó")
	}
}

// Si quien reportó responde estando resuelto, ya no se cierra solo. Quien atiende no lo retiene.
func TestReporterReplyHoldsAutoClose(t *testing.T) {
	sv, _ := newAccount(t)
	inc, _ := sv.Incidents.Create(tid, guest, vpn("x"))
	must(t, sv.Incidents.Comment(tid, guest, inc.ID, "antes de resolver", true))
	must(t, sv.Incidents.Update(tid, agent, inc.ID, resolve("solucionado", "ok")))
	must(t, sv.Incidents.Comment(tid, agent, inc.ID, "queda resuelto", true))
	if inc.AutoCloseHold {
		t.Fatal("solo retiene la respuesta de quien reportó, y solo estando resuelto")
	}
	must(t, sv.Incidents.Comment(tid, guest, inc.ID, "a mí me sigue fallando", true))
	if !inc.AutoCloseHold || last(inc).Kind != domain.EvReply {
		t.Fatalf("la respuesta de quien reportó en un resuelto lo retiene: %+v", inc)
	}
}

// Un incidente nuevo puede continuar uno cerrado: tiene que existir y estar cerrado, y quien solo reporta,
// relacionarlo con uno suyo. Queda en el historial de los dos.
func TestRelatedIncident(t *testing.T) {
	sv, tn := newAccount(t)
	mine, _ := sv.Incidents.Create(tid, guest, vpn("mío"))
	theirs, _ := sv.Incidents.Create(tid, agent, vpn("ajeno"))
	related := func(by, id int) (*domain.Incident, error) {
		in := vpn("volvió a pasar")
		in.RelatedIncidentID = id
		return sv.Incidents.Create(tid, by, in)
	}
	for _, id := range []int{mine.ID, 99} {
		if _, err := related(guest, id); err == nil || errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("#%d no está cerrado o no existe (400): %v", id, err)
		}
	}
	for _, inc := range []*domain.Incident{mine, theirs} {
		must(t, sv.Incidents.Update(tid, agent, inc.ID, resolve("solucionado", "ok")))
		must(t, sv.Incidents.Close(tid, agent, inc.ID))
	}
	count := len(tn.Incidents)
	if _, err := related(guest, theirs.ID); !errors.Is(err, domain.ErrForbidden) || len(tn.Incidents) != count {
		t.Fatalf("quien solo reporta no lo relaciona con uno ajeno: %v", err)
	}

	inc, err := related(guest, mine.ID)
	must(t, err)
	if inc.RelatedIncidentID != mine.ID || inc.History[0].Text != fmt.Sprintf("Incidente registrado (relacionado con #%d)", mine.ID) {
		t.Fatalf("incidente relacionado mal registrado: %+v", inc)
	}
	if e := last(mine); e.Kind != domain.EvRelated || e.Text != fmt.Sprintf("Se registró #%d relacionado", inc.ID) || e.UserID != guest {
		t.Fatalf("el cerrado tiene que registrar la relación: %+v", e)
	}
	if _, err := related(agent, mine.ID); err != nil {
		t.Fatalf("quien resuelve lo relaciona con cualquier cerrado: %v", err)
	}
}

// Respuestas y notas: quien atiende publica las dos; quien reportó, solo respuestas en los suyos.
func TestComments(t *testing.T) {
	sv, _ := newAccount(t)
	mine, _ := sv.Incidents.Create(tid, guest, vpn("mío"))
	other, _ := sv.Incidents.Create(tid, agent, vpn("de otro"))

	must(t, sv.Incidents.Comment(tid, guest, mine.ID, "gracias", true))
	if e := last(mine); e.Kind != domain.EvReply || e.UserID != guest || mine.FirstResponseAt != nil {
		t.Fatalf("la respuesta de quien reportó no es la primera respuesta: %+v", mine)
	}
	must(t, sv.Incidents.Comment(tid, agent, mine.ID, " lo estoy mirando ", true))
	if e := last(mine); e.Kind != domain.EvReply || e.Text != "lo estoy mirando" || mine.FirstResponseAt == nil || !mine.FirstResponseAt.Equal(e.At) {
		t.Fatalf("respuesta mal registrada: %+v", mine)
	}
	must(t, sv.Incidents.Comment(tid, agent, mine.ID, "parece el concentrador", false))
	if last(mine).Kind != domain.EvNote {
		t.Fatalf("esperaba una nota: %+v", last(mine))
	}

	before := len(mine.History)
	if err := sv.Incidents.Comment(tid, guest, mine.ID, "x", false); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("quien reportó no deja notas internas: %v", err)
	}
	if err := sv.Incidents.Comment(tid, guest, other.ID, "x", true); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("quien reportó no comenta incidentes ajenos: %v", err)
	}
	if err := sv.Incidents.Comment(tid, agent, mine.ID, "  ", true); err == nil || errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("el texto vacío es un error de validación: %v", err)
	}
	if len(mine.History) != before {
		t.Fatal("los comentarios rechazados no deben quedar en el historial")
	}
}

// Quien solo reporta no ve las notas internas ni los eventos de problema, y filtrarlos no toca la cuenta.
func TestViewForReporter(t *testing.T) {
	sv, _ := newAccount(t)
	mine, _ := sv.Incidents.Create(tid, guest, vpn("mío"))
	p, err := sv.Problems.Create(tid, agent, domain.NewProblem{Title: "p", Service: "VPN", IncidentIDs: []int{mine.ID}})
	must(t, err)
	none := 0
	must(t, sv.Incidents.Update(tid, agent, mine.ID, domain.IncidentPatch{ProblemID: &none}))
	must(t, sv.Incidents.Update(tid, agent, mine.ID, domain.IncidentPatch{ProblemID: &p.ID}))
	must(t, sv.Incidents.Comment(tid, agent, mine.ID, "interna", false))
	must(t, sv.Incidents.Comment(tid, agent, mine.ID, "pública", true))
	// creado, vinculado, desvinculado, vinculado, nota y respuesta
	kinds := func(h []domain.Event) (n map[string]int) {
		n = map[string]int{}
		for _, e := range h {
			n[e.Kind]++
		}
		return n
	}

	v, _ := sv.State.View(tid, guest)
	if k := kinds(v.Incidents[0].History); k[domain.EvNote] != 0 || k[domain.EvProblem] != 0 || len(v.Incidents[0].History) != 2 {
		t.Fatalf("el solicitante ve notas o eventos de problema: %+v", v.Incidents[0].History)
	}
	if k := kinds(mine.History); len(mine.History) != 6 || k[domain.EvProblem] != 3 || k[domain.EvNote] != 1 {
		t.Fatalf("la vista modificó la cuenta: %+v", mine.History)
	}
	if v, _ := sv.State.View(tid, agent); len(v.Incidents[0].History) != 6 {
		t.Fatal("quien atiende ve las notas y los eventos de problema")
	}
}

// Solo se asigna un incidente a quien puede atenderlo (resolver). 0 = sin asignar vale siempre.
func TestAssignRequiresResolver(t *testing.T) {
	sv, tn := newAccount(t)
	in := vpn("asignado a quien solo reporta")
	in.AssigneeID = guest
	before := len(tn.Incidents)
	if _, err := sv.Incidents.Create(tid, agent, in); err == nil || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrForbidden) || len(tn.Incidents) != before {
		t.Fatalf("el alta asignada al solicitante es un error de validación y no crea nada: err=%v, %d -> %d", err, before, len(tn.Incidents))
	}
	in.AssigneeID = 0
	if inc, err := sv.Incidents.Create(tid, agent, in); err != nil || inc.AssigneeID != 0 {
		t.Fatalf("sin asignar siempre se puede: %v", err)
	}
	in.AssigneeID = agent
	inc, err := sv.Incidents.Create(tid, owner, in)
	if err != nil || inc.AssigneeID != agent {
		t.Fatalf("al agente se le puede asignar: %v", err)
	}

	who, enCurso, events := guest, "En curso", len(inc.History)
	if err := sv.Incidents.Update(tid, agent, inc.ID, domain.IncidentPatch{AssigneeID: &who, Status: &enCurso}); err == nil || errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("el patch asignado al solicitante es un error de validación: %v", err)
	}
	if inc.AssigneeID != agent || inc.Status != "Nuevo" || len(inc.History) != events {
		t.Fatalf("el patch rechazado no debe aplicar nada: asignado=%d estado=%q eventos=%d", inc.AssigneeID, inc.Status, len(inc.History))
	}
	for _, who := range []int{0, agent} {
		if err := sv.Incidents.Update(tid, agent, inc.ID, domain.IncidentPatch{AssigneeID: &who}); err != nil || inc.AssigneeID != who {
			t.Fatalf("asignar a %d: asignado=%d %v", who, inc.AssigneeID, err)
		}
	}
}

// La primera respuesta es la primera respuesta pública de quien atiende, una sola vez.
// Asignar, cambiar el estado o dejar una nota no cuentan.
func TestFirstResponse(t *testing.T) {
	sv, _ := newAccount(t)
	inc, _ := sv.Incidents.Create(tid, agent, vpn("x"))
	who := agent
	must(t, sv.Incidents.Update(tid, agent, inc.ID, domain.IncidentPatch{AssigneeID: &who}))
	must(t, sv.Incidents.Update(tid, agent, inc.ID, domain.IncidentPatch{Status: &enCurso}))
	must(t, sv.Incidents.Comment(tid, agent, inc.ID, "interna", false))
	if inc.FirstResponseAt != nil {
		t.Fatal("asignar, cambiar el estado o dejar una nota no es responder")
	}
	must(t, sv.Incidents.Comment(tid, agent, inc.ID, "hola", true))
	if inc.FirstResponseAt == nil || !inc.FirstResponseAt.Equal(last(inc).At) {
		t.Fatal("la respuesta pública de quien atiende es la primera respuesta")
	}
	first := *inc.FirstResponseAt
	must(t, sv.Incidents.Comment(tid, agent, inc.ID, "otra", true))
	if !inc.FirstResponseAt.Equal(first) {
		t.Fatal("la primera respuesta se fija una sola vez")
	}
}

// pauseIn configura la cuenta con estados de pausa.
func pauseIn(t *testing.T, sv *service.Services, tn *domain.Tenant, states ...string) {
	t.Helper()
	c := tn.Config
	c.PauseStates = states
	must(t, sv.Setup.SetConfig(tid, owner, c))
}

// backdate corre el comienzo de la pausa hacia atrás, como si llevara ese tiempo pausado.
func backdate(inc *domain.Incident, d time.Duration) time.Time {
	since := inc.PausedAt.Add(-d)
	inc.PausedAt = &since
	return since
}

// La pausa corre el vencimiento de resolución exactamente lo que duró, también si se resuelve desde la pausa.
// El de la primera respuesta no se mueve nunca, aunque la respuesta llegue durante la pausa.
func TestPause(t *testing.T) {
	sv, tn := newAccount(t)
	pauseIn(t, sv, tn, enCurso)
	inc, _ := sv.Incidents.Create(tid, agent, vpn("x"))
	due, resp := *inc.DueAt, *inc.ResponseDueAt

	must(t, sv.Incidents.Update(tid, agent, inc.ID, domain.IncidentPatch{Status: &enCurso}))
	if inc.PausedAt == nil || !inc.PausedAt.Equal(last(inc).At) {
		t.Fatal("al entrar a un estado de pausa el reloj se detiene")
	}
	since := backdate(inc, 2*time.Hour)
	must(t, sv.Incidents.Update(tid, agent, inc.ID, domain.IncidentPatch{Status: &nuevo}))
	if paused := last(inc).At.Sub(since); inc.PausedAt != nil || inc.DueAt.Sub(due) != paused || !inc.ResponseDueAt.Equal(resp) {
		t.Fatalf("pausa de %v: dueAt corrió %v, responseDueAt %v -> %v", paused, inc.DueAt.Sub(due), resp, inc.ResponseDueAt)
	}

	// Primera respuesta durante la pausa, y resolución directa desde la pausa.
	must(t, sv.Incidents.Update(tid, agent, inc.ID, domain.IncidentPatch{Status: &enCurso}))
	due, since = *inc.DueAt, backdate(inc, 3*time.Hour)
	must(t, sv.Incidents.Comment(tid, agent, inc.ID, "hola", true))
	if inc.PausedAt == nil || inc.FirstResponseAt == nil || !inc.ResponseDueAt.Equal(resp) {
		t.Fatalf("la respuesta en pausa no mueve su vencimiento: %v -> %v", resp, inc.ResponseDueAt)
	}
	must(t, sv.Incidents.Update(tid, agent, inc.ID, resolve("workaround", "vpn2")))
	if inc.PausedAt != nil || inc.DueAt.Sub(due) != inc.ResolvedAt.Sub(since) || !inc.ResponseDueAt.Equal(resp) {
		t.Fatalf("resolver desde la pausa: corrió %v, esperaba %v; respuesta %v -> %v",
			inc.DueAt.Sub(due), inc.ResolvedAt.Sub(since), resp, inc.ResponseDueAt)
	}
}

// SetConfig valida los estados de pausa, los renombra con su estado y recalcula la pausa de los abiertos.
func TestConfigPauseStates(t *testing.T) {
	sv, tn := newAccount(t)
	for _, bad := range [][]string{{nuevo}, {resuelto}, {"No existe"}, {enCurso, enCurso}} {
		_, c, _ := service.BuildConfig("it", service.Full)
		c.PauseStates = bad
		if sv.Setup.SetConfig(tid, owner, c) == nil {
			t.Fatalf("estados de pausa inválidos: %v", bad)
		}
	}
	for _, change := range []func(c *domain.Config){
		func(c *domain.Config) { c.ResponseHours["media"] = 0 },
		func(c *domain.Config) { c.ResponseHours = nil },
		func(c *domain.Config) { c.AutoCloseDays = 0 },
	} {
		_, c, _ := service.BuildConfig("it", service.Full)
		change(&c)
		if sv.Setup.SetConfig(tid, owner, c) == nil {
			t.Fatalf("configuración inválida aceptada: %+v", c)
		}
	}

	// Renombrar un estado de pausa: llega con el nombre viejo y queda con el nuevo; el incidente que está ahí se pausa.
	busy, _ := sv.Incidents.Create(tid, agent, vpn("en curso"))
	must(t, sv.Incidents.Update(tid, agent, busy.ID, domain.IncidentPatch{Status: &enCurso}))
	_, c, _ := service.BuildConfig("it", service.Full)
	c.States[1], c.PauseStates = "Esperando", []string{enCurso}
	must(t, sv.Setup.SetConfig(tid, owner, c))
	if fmt.Sprint(tn.Config.PauseStates) != "[Esperando]" || busy.Status != "Esperando" || busy.PausedAt == nil {
		t.Fatalf("pausa=%v estado=%q pausedAt=%v", tn.Config.PauseStates, busy.Status, busy.PausedAt)
	}

	// Un incidente que queda en un estado de pausa porque el inicial pasa a ser otro.
	fresh, _ := sv.Incidents.Create(tid, agent, vpn("sin respuesta"))
	_, c, _ = service.BuildConfig("it", service.Full)
	c.States, c.PauseStates = []string{"Triage", nuevo, resuelto}, []string{nuevo}
	must(t, sv.Setup.SetConfig(tid, owner, c))
	if fresh.Status != nuevo || fresh.PausedAt == nil || busy.Status != "Triage" || busy.PausedAt != nil {
		t.Fatalf("recalcular la pausa: %q %v · %q %v", fresh.Status, fresh.PausedAt, busy.Status, busy.PausedAt)
	}
	due, resp, since := *fresh.DueAt, *fresh.ResponseDueAt, backdate(fresh, time.Hour)
	c = tn.Config
	c.PauseStates = []string{}
	from := time.Now()
	must(t, sv.Setup.SetConfig(tid, owner, c))
	to := time.Now()
	moved := fresh.DueAt.Sub(due)
	if fresh.PausedAt != nil || moved < from.Sub(since) || moved > to.Sub(since) || !fresh.ResponseDueAt.Equal(resp) {
		t.Fatalf("al reanudar se corre solo el vencimiento de resolución: %v, respuesta %v -> %v", moved, resp, fresh.ResponseDueAt)
	}
}

// La solución definitiva de un problema resuelve sus incidentes con código "solucionado" y la solución como
// única nota (sin cerrarlos ni contar como primera respuesta), y cierra su pausa.
func TestProblemResolvesIncidents(t *testing.T) {
	sv, tn := newAccount(t)
	pauseIn(t, sv, tn, enCurso)
	a, _ := sv.Incidents.Create(tid, agent, vpn("a"))
	must(t, sv.Incidents.Update(tid, agent, a.ID, domain.IncidentPatch{Status: &enCurso}))
	p, err := sv.Problems.Create(tid, agent, domain.NewProblem{Title: "p", Service: "VPN", IncidentIDs: []int{a.ID}})
	must(t, err)
	if e := last(a); e.Kind != domain.EvProblem || e.Text != "Vinculado al problema P-1" {
		t.Fatalf("crear el problema desde el incidente lo vincula: %+v", e)
	}
	must(t, sv.Problems.Advance(tid, agent, p.ID, domain.Advance{}))
	must(t, sv.Problems.Advance(tid, agent, p.ID, domain.Advance{RootCause: "licencias"}))
	must(t, sv.Problems.Advance(tid, agent, p.ID, domain.Advance{Solution: " más licencias "}))
	if a.Status != resuelto || a.ResolvedAt == nil || a.Closed() || a.ResolutionCode != "solucionado" || a.PausedAt != nil ||
		a.FirstResponseAt != nil || a.ResolutionNote != "más licencias" ||
		last(a).Text != "De En curso a Resuelto · Solucionado: más licencias" ||
		a.History[len(a.History)-2].Kind != domain.EvProblem {
		t.Fatalf("resolución por el problema (un solo evento): %+v", a)
	}
}

// No se le saca el permiso resolver a quien tiene incidentes abiertos asignados: primero hay que reasignarlos.
// Los resueltos no cuentan, y un rol sin usuarios se edita libremente.
func TestDropResolveNeedsReassign(t *testing.T) {
	sv, tn := newAccount(t)
	in := vpn("abierto")
	in.AssigneeID = agent
	open, err := sv.Incidents.Create(tid, owner, in)
	must(t, err)
	done, err := sv.Incidents.Create(tid, owner, in)
	must(t, err)
	must(t, sv.Incidents.Update(tid, agent, done.ID, resolve("solucionado", "ok")))

	err = sv.Accounts.SetUserRole(tid, owner, agent, 3)
	if err == nil || errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrNotFound) ||
		err.Error() != "sin el permiso resolver no se pueden tener incidentes abiertos asignados: reasigná primero los de Agente (#1)" {
		t.Fatalf("pasar a Solicitante a quien tiene un abierto asignado: %v", err)
	}
	if tn.User(agent).RoleID != 2 {
		t.Fatal("el cambio de rol rechazado no debe aplicarse")
	}
	perms := append([]string{}, tn.Role(2).Perms...)
	if err := sv.Accounts.SaveRole(tid, owner, 2, "Agente", []string{domain.PermPanel, domain.PermCreate}); err == nil || errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("sacarle resolver al rol de quien tiene un abierto asignado: %v", err)
	}
	if fmt.Sprint(tn.Role(2).Perms) != fmt.Sprint(perms) {
		t.Fatalf("la edición rechazada no debe aplicarse: %v", tn.Role(2).Perms)
	}

	// Lo que no le saca resolver no se bloquea.
	must(t, sv.Accounts.SaveRole(tid, owner, 2, "Técnico", perms))
	must(t, sv.Accounts.SaveRole(tid, owner, 0, "Soporte", []string{domain.PermResolve}))
	soporte := tn.Roles[len(tn.Roles)-1].ID
	must(t, sv.Accounts.SetUserRole(tid, owner, agent, soporte))
	// Un rol sin usuarios pierde resolver sin problema.
	must(t, sv.Accounts.SaveRole(tid, owner, 2, "Técnico", []string{domain.PermCreate}))

	// Reasignado el abierto, ya se puede (el resuelto asignado no cuenta).
	none := 0
	must(t, sv.Incidents.Update(tid, owner, open.ID, domain.IncidentPatch{AssigneeID: &none}))
	must(t, sv.Accounts.SetUserRole(tid, owner, agent, 3))
	if done.AssigneeID != agent || tn.User(agent).RoleID != 3 {
		t.Fatalf("asignado del resuelto=%d rol=%d", done.AssigneeID, tn.User(agent).RoleID)
	}
}

// El error de SaveRole nombra a todos los usuarios del rol que tienen incidentes abiertos asignados.
func TestDropResolveNamesEveryone(t *testing.T) {
	sv, _ := newAccount(t)
	must(t, sv.Accounts.AddUser(tid, owner, "Agente 2", "agente2@demo.test", pass, 2))
	for _, who := range []int{agent, 4, agent} {
		in := vpn("x")
		in.AssigneeID = who
		_, err := sv.Incidents.Create(tid, owner, in)
		must(t, err)
	}
	err := sv.Accounts.SaveRole(tid, owner, 2, "Agente", []string{domain.PermPanel})
	if err == nil || err.Error() != "sin el permiso resolver no se pueden tener incidentes abiertos asignados: reasigná primero los de Agente (#1, #3) y Agente 2 (#2)" {
		t.Fatalf("%v", err)
	}
}

// El responsable de un problema tiene que poder gestionar problemas.
func TestProblemOwnerNeedsPermission(t *testing.T) {
	sv, _ := newAccount(t)
	p, err := sv.Problems.Create(tid, agent, domain.NewProblem{Title: "p", Service: "VPN"})
	must(t, err)
	events := len(p.History)
	err = sv.Problems.SetOwner(tid, agent, p.ID, guest)
	if err == nil || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrForbidden) || p.OwnerID != agent || len(p.History) != events {
		t.Fatalf("el solicitante no puede ser responsable: err=%v responsable=%d eventos=%d", err, p.OwnerID, len(p.History))
	}
	if err := sv.Problems.SetOwner(tid, agent, p.ID, 99); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("un usuario inexistente sigue siendo 404: %v", err)
	}
	if err := sv.Problems.SetOwner(tid, agent, p.ID, owner); err != nil || p.OwnerID != owner {
		t.Fatalf("el titular sí puede: %v", err)
	}
}

// Reabrir un incidente cuyo asignado perdió resolver mientras estaba resuelto lo deja sin asignar, con el motivo,
// después del evento de reapertura. Si el asignado sigue pudiendo atenderlo, lo conserva.
func TestReopenUnassignsWithoutResolve(t *testing.T) {
	sv, _ := newAccount(t)
	in := vpn("x")
	in.AssigneeID = agent
	inc, err := sv.Incidents.Create(tid, owner, in)
	must(t, err)
	in.AssigneeID = owner
	kept, err := sv.Incidents.Create(tid, owner, in)
	must(t, err)
	for _, i := range []*domain.Incident{inc, kept} {
		must(t, sv.Incidents.Update(tid, owner, i.ID, resolve("solucionado", "ok")))
	}
	must(t, sv.Accounts.SetUserRole(tid, owner, agent, 3)) // el resuelto no bloquea el cambio
	if inc.AssigneeID != agent {
		t.Fatal("el cambio de rol no toca el asignado")
	}

	before := len(inc.History)
	must(t, sv.Incidents.Reopen(tid, owner, inc.ID, "volvió a pasar"))
	h := inc.History
	if inc.AssigneeID != 0 || len(h) != before+2 ||
		h[len(h)-2].Kind != domain.EvReopened || last(inc).Kind != domain.EvAssigned ||
		last(inc).Text != "Sin asignar (Agente ya no atiende incidentes)" || last(inc).UserID != owner || !last(inc).At.Equal(h[len(h)-2].At) {
		t.Fatalf("al reabrir vuelve sin asignar: asignado=%d %+v", inc.AssigneeID, h[before:])
	}
	before = len(kept.History)
	must(t, sv.Incidents.Reopen(tid, owner, kept.ID, "volvió a pasar"))
	if kept.AssigneeID != owner || len(kept.History) != before+1 {
		t.Fatalf("quien sigue con resolver conserva el incidente: asignado=%d", kept.AssigneeID)
	}
}

// No se le saca el permiso problemas a quien es responsable de un problema activo: primero hay que pasarlo a
// otro responsable. Los resueltos no cuentan. Si pierde resolver y problemas a la vez, el error lista las dos cosas.
func TestDropProblemsNeedsHandover(t *testing.T) {
	sv, tn := newAccount(t)
	for _, title := range []string{"a", "b", "c"} {
		_, err := sv.Problems.Create(tid, agent, domain.NewProblem{Title: title, Service: "VPN"})
		must(t, err)
	}
	must(t, sv.Problems.Advance(tid, agent, 2, domain.Advance{}))
	must(t, sv.Problems.Advance(tid, agent, 2, domain.Advance{RootCause: "x"}))
	must(t, sv.Problems.Advance(tid, agent, 2, domain.Advance{Solution: "y"})) // P-2 resuelto
	must(t, sv.Accounts.SaveRole(tid, owner, 0, "Atención", []string{domain.PermResolve}))
	atencion := tn.Roles[len(tn.Roles)-1].ID
	only := "sin el permiso problemas no se puede ser responsable de un problema activo: pasá primero P-1 y P-3 de Agente a otro responsable"

	if err := sv.Accounts.SetUserRole(tid, owner, agent, atencion); err == nil || err.Error() != only || tn.User(agent).RoleID != 2 {
		t.Fatalf("pasar a un rol sin problemas al responsable de problemas activos: %v", err)
	}
	perms := fmt.Sprint(tn.Role(2).Perms)
	if err := sv.Accounts.SaveRole(tid, owner, 2, "Agente", []string{domain.PermResolve}); err == nil || err.Error() != only || fmt.Sprint(tn.Role(2).Perms) != perms {
		t.Fatalf("sacarle problemas al rol del responsable de problemas activos: %v", err)
	}

	in := vpn("x")
	in.AssigneeID = agent
	_, err := sv.Incidents.Create(tid, owner, in)
	must(t, err)
	both := "sin el permiso resolver no se pueden tener incidentes abiertos asignados: reasigná primero los de Agente (#1); " + only
	if err := sv.Accounts.SetUserRole(tid, owner, agent, 3); err == nil || err.Error() != both {
		t.Fatalf("si pierde los dos, el error lista los dos: %v", err)
	}
	if err := sv.Accounts.SaveRole(tid, owner, 2, "Agente", []string{domain.PermPanel}); err == nil || err.Error() != both {
		t.Fatalf("si el rol pierde los dos, el error lista los dos: %v", err)
	}

	must(t, sv.Problems.SetOwner(tid, owner, 1, owner))
	must(t, sv.Problems.SetOwner(tid, owner, 3, owner))
	must(t, sv.Accounts.SetUserRole(tid, owner, agent, atencion)) // sin problemas activos: pasa (P-2 está resuelto)
}

// Con datos viejos (alguien sin el permiso que ya tiene asignado algo), cambiar de rol solo se bloquea si
// el cambio le saca el permiso.
func TestRoleChangeBlocksOnlyWhatItTakes(t *testing.T) {
	sv, tn := newAccount(t)
	inc, err := sv.Incidents.Create(tid, owner, vpn("x"))
	must(t, err)
	p, err := sv.Problems.Create(tid, agent, domain.NewProblem{Title: "p", Service: "VPN"})
	must(t, err)
	inc.AssigneeID, p.OwnerID = guest, guest // como quedaron antes de estas reglas
	must(t, sv.Accounts.SaveRole(tid, owner, 0, "Lectura", []string{domain.PermPanel}))
	must(t, sv.Accounts.SetUserRole(tid, owner, guest, tn.Roles[len(tn.Roles)-1].ID))
}

// Al eliminar a un usuario sus resueltos siguen asignados a él (DeleteUser solo desasigna los abiertos).
// Reabrir uno lo deja sin asignar, después del evento de reapertura.
func TestReopenUnassignsDeletedUser(t *testing.T) {
	sv, _ := newAccount(t)
	in := vpn("x")
	in.AssigneeID = agent
	inc, err := sv.Incidents.Create(tid, owner, in)
	must(t, err)
	must(t, sv.Incidents.Update(tid, owner, inc.ID, resolve("solucionado", "ok")))
	must(t, sv.Accounts.DeleteUser(tid, owner, agent))
	if inc.AssigneeID != agent {
		t.Fatalf("eliminar al usuario no toca sus resueltos: asignado=%d", inc.AssigneeID)
	}

	before := len(inc.History)
	must(t, sv.Incidents.Reopen(tid, owner, inc.ID, "volvió a pasar"))
	h := inc.History
	if inc.AssigneeID != 0 || len(h) != before+2 ||
		h[len(h)-2].Kind != domain.EvReopened || last(inc).Kind != domain.EvAssigned ||
		last(inc).Text != "Sin asignar (el usuario asignado ya no existe)" || last(inc).UserID != owner || !last(inc).At.Equal(h[len(h)-2].At) {
		t.Fatalf("al reabrir vuelve sin asignar: asignado=%d %+v", inc.AssigneeID, h[before:])
	}

	// Sin asignado, reabrir no agrega el evento.
	must(t, sv.Incidents.Update(tid, owner, inc.ID, resolve("solucionado", "ok")))
	before = len(inc.History)
	must(t, sv.Incidents.Reopen(tid, owner, inc.ID, "otra vez"))
	if len(inc.History) != before+1 || last(inc).Kind != domain.EvReopened {
		t.Fatalf("sin asignado solo queda la reapertura: %+v", inc.History[before:])
	}
}
