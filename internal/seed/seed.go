// Package seed arma las cuentas de demo con las que arranca una instalación vacía.
package seed

import (
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"time"

	"taas-backend/internal/domain"
	"taas-backend/internal/service"
)

type seedProblem struct {
	service, title, desc string
	stage                int // 1 en análisis · 2 error conocido · 3 resuelto
	rootCause, wa, sol   string
}

type seedTenant struct {
	name, template string
	domain         string
	users          []string            // titular, dos agentes y un solicitante
	titles         map[string][]string // por servicio
	problems       []seedProblem
	recent         string // servicio con incidentes recientes sin investigar: dispara la sugerencia
}

var seeds = []seedTenant{
	{
		name: "Nexo Soporte IT", template: "it", domain: "nexo.demo",
		users: []string{"Ana Gómez", "Martín Ruiz", "Lucía Pérez", "Sofía Vidal"},
		titles: map[string][]string{
			"VPN":             {"La VPN se desconecta cada 10 minutos", "No puedo conectarme a la VPN desde casa", "VPN caída a primera hora"},
			"Correo":          {"Los clientes no reciben nuestros mails", "Mails a Gmail rebotan como spam", "No llegan los correos a proveedores"},
			"ERP Facturación": {"El ERP tarda 2 minutos en emitir una factura", "Timeout al cerrar el lote de facturas", "Se cuelga el ERP al confirmar la factura"},
			"Impresoras":      {"La impresora del 2° piso no imprime", "Trabajos trabados en la cola de impresión", "Otra vez no anda la impresora del 2° piso"},
			"WiFi":            {"WiFi lento en la sala de reuniones", "Se corta el WiFi en recepción"},
		},
		problems: []seedProblem{
			{"VPN", "Caídas intermitentes de la VPN", "Los usuarios remotos pierden la conexión en horarios pico.", 2,
				"El concentrador VPN agota su pool de 50 licencias concurrentes entre las 9 y las 11 hs.",
				"Reconectarse usando el gateway secundario: vpn2.empresa.com", ""},
			{"ERP Facturación", "Lentitud del ERP al facturar", "Empeora a fin de mes, cuando crece el volumen.", 1, "", "", ""},
			{"Correo", "Correo saliente rechazado", "Los correos hacia dominios externos rebotan o llegan como spam.", 3,
				"El registro SPF del dominio no incluía al nuevo proveedor de envío.",
				"Enviar los mails urgentes desde la cuenta de respaldo.", "Se actualizó el registro SPF y se configuró DKIM."},
		},
		recent: "Impresoras",
	},
	{
		name: "Clínica del Sol", template: "salud", domain: "clinicadelsol.demo",
		users: []string{"Carla Méndez", "Diego Sosa", "Paula Ibarra", "Tomás Rey"},
		titles: map[string][]string{
			"Historia clínica electrónica": {"La historia clínica no abre en la guardia", "Error al guardar la evolución del paciente", "La HCE se cierra sola al cambiar de turno"},
			"Turnos online":                {"El portal de turnos muestra error de seguridad", "Los pacientes no pueden sacar turno desde el celular", "No llegan los recordatorios de turno"},
			"Equipamiento de imágenes":     {"El tomógrafo no envía estudios al visor", "Las placas tardan 20 minutos en aparecer", "El ecógrafo 2 no exporta imágenes"},
			"Laboratorio":                  {"Los resultados no se cargan en la historia clínica", "El analizador no toma las órdenes del sistema", "Etiquetas de muestras con datos cortados"},
			"Facturación a obras sociales": {"Rechazo masivo de la presentación mensual", "No se puede validar la credencial del afiliado"},
		},
		problems: []seedProblem{
			{"Historia clínica electrónica", "La HCE deja de responder en el cambio de guardia", "Entre las 7 y las 8 y entre las 19 y las 20 los médicos no pueden abrir ni guardar historias.", 2,
				"El servidor de la HCE agota las conexiones a la base cuando se superponen las sesiones del turno saliente y del entrante.",
				"Reingresar después de 2 minutos. Para consultas urgentes, usar el modo de solo lectura.", ""},
			{"Equipamiento de imágenes", "Demora en la llegada de estudios al visor", "Los estudios tardan en estar disponibles para informar.", 1, "", "", ""},
			{"Turnos online", "Portal de turnos inaccesible", "Los navegadores bloqueaban el portal con una advertencia de seguridad.", 3,
				"El certificado SSL del portal venció: no tenía renovación automática ni alerta previa.",
				"Dar los turnos por teléfono mientras dure el corte.", "Renovación automática del certificado y alerta 30 días antes del vencimiento."},
		},
		recent: "Laboratorio",
	},
}

// DemoPassword: contraseña de todos los usuarios de demo (datos de prueba; ver README).
const DemoPassword = "demo-taas-2026"

// seedEmail: "Ana Gómez" en nexo.demo -> ana@nexo.demo
func seedEmail(name, host string) string {
	first := []rune{}
	for _, r := range name {
		if r == ' ' {
			break
		}
		first = append(first, r)
	}
	ascii := map[rune]rune{'á': 'a', 'é': 'e', 'í': 'i', 'ó': 'o', 'ú': 'u'}
	out := []rune{}
	for _, r := range []rune(domain.Slug(string(first))) {
		if a, ok := ascii[r]; ok {
			r = a
		}
		out = append(out, r)
	}
	return string(out) + "@" + host
}

// Tenants arma las cuentas de demo, con un mes de actividad, el historial de cada incidente y un problema en cada etapa.
func Tenants() []*domain.Tenant {
	rnd := rand.New(rand.NewSource(7)) // fijo: la demo arranca siempre igual
	// El ciclo de vida usa su propia semilla, también fija, para no cambiar los incidentes y problemas de siempre.
	life := rand.New(rand.NewSource(11))
	now := time.Now()
	hash := service.HashPassword(DemoPassword) // una sola vez: derivarla es caro a propósito
	out := []*domain.Tenant{}
	for i, sd := range seeds {
		industry, cfg, _ := service.BuildConfig(sd.template, service.Full)
		t := &domain.Tenant{ID: i + 1, Name: sd.name, Industry: industry, Onboarded: true, OwnerID: 1, Config: cfg,
			Roles: service.DefaultRoles(), NextUserID: len(sd.users) + 1, NextRoleID: 4, Problems: []*domain.Problem{}}
		for j, name := range sd.users {
			role := []int{1, 2, 2, 3}[j] // titular, agente, agente, solicitante
			t.Users = append(t.Users, domain.User{ID: j + 1, Name: name, Email: seedEmail(name, sd.domain), RoleID: role, Hash: hash})
		}
		final := t.ResolvedState()
		byProblem := map[int]string{}

		// 45 incidentes repartidos en los últimos 30 días, más 3 recientes del servicio sin investigar.
		ages := []float64{30, 9, 0.5} // horas
		for k := 0; k < 45; k++ {
			ages = append(ages, 36+rnd.Float64()*24*29)
		}
		sort.Sort(sort.Reverse(sort.Float64Slice(ages)))
		for _, age := range ages {
			service := sd.recent
			if age > 31 {
				service = t.Config.Services[rnd.Intn(len(t.Config.Services))]
			}
			prio := domain.Priorities[[]int{0, 1, 1, 2}[rnd.Intn(4)]]
			created := now.Add(-time.Duration(age * float64(time.Hour)))
			sla := time.Duration(t.Config.SLAHours[prio]) * time.Hour
			due := created.Add(sla)
			inc := &domain.Incident{ID: len(t.Incidents) + 1, Title: sd.titles[service][rnd.Intn(len(sd.titles[service]))],
				Service: service, Priority: prio, Status: t.Config.States[rnd.Intn(len(t.Config.States)-1)],
				Fields: map[string]string{}, CreatedBy: 1 + rnd.Intn(len(t.Users)), AssigneeID: rnd.Intn(4), CreatedAt: created, DueAt: &due}
			for _, f := range t.Config.Fields {
				switch f.Type {
				case "select":
					inc.Fields[f.Key] = f.Options[rnd.Intn(len(f.Options))]
				case "number":
					inc.Fields[f.Key] = []string{"3", "12", "25", "40"}[rnd.Intn(4)]
				}
			}
			// La mayoría de los viejos ya se resolvió; cada tanto, fuera de SLA.
			if done := created.Add(time.Duration((0.1 + rnd.Float64()*1.05) * float64(sla))); age > 31 && rnd.Float64() < 0.93 && done.Before(now) {
				inc.Status, inc.ResolvedAt = final, &done
			}
			t.Incidents = append(t.Incidents, inc)
		}

		for _, sp := range sd.problems {
			p := &domain.Problem{ID: len(t.Problems) + 1, Title: sp.title, Description: sp.desc, Service: sp.service, OwnerID: 2}
			var linked []*domain.Incident
			for _, inc := range t.Incidents {
				if inc.Service == sp.service {
					inc.ProblemID = p.ID
					linked = append(linked, inc)
				}
			}
			// El problema se abre con el segundo incidente del servicio y avanza una etapa por día.
			at := linked[1].CreatedAt.Add(time.Hour)
			step := func(kind, text string, by int) {
				if at.After(now) {
					at = now
				}
				p.History = append(p.History, domain.Event{At: at, Kind: kind, Text: text, UserID: by})
				at = at.Add(26 * time.Hour)
			}
			// Al crearlo se vinculan los incidentes que ya estaban registrados (ver lifecycle); los demás, al registrarse.
			first := 0
			for _, inc := range linked {
				if inc.CreatedAt.Add(10 * time.Minute).Before(at) {
					first++
				}
			}
			p.CreatedAt, p.Status = at, domain.Identificado
			step("", fmt.Sprintf("Problema creado desde %d incidentes recurrentes", first), 1)
			if sp.stage >= 1 {
				p.Status = domain.EnAnalisis
				step("analisis", "Análisis de causa raíz iniciado", 2)
			}
			if sp.stage >= 2 {
				p.Status, p.RootCause, p.Workaround = domain.ErrorConocido, sp.rootCause, sp.wa
				step("conocido", "Registrado como error conocido", 2)
			}
			if sp.stage == 3 {
				p.Status, p.Solution = domain.Resuelto, sp.sol
				at = now.Add(-20 * time.Hour)
				// Como en la API: la solución definitiva resuelve los incidentes abiertos en el mismo momento.
				for _, inc := range linked {
					if inc.ResolvedAt == nil {
						done := at
						byProblem[inc.ID] = inc.Status
						inc.Status, inc.ResolvedAt = final, &done
					}
				}
				step("resuelto", "Resuelto · incidentes vinculados resueltos", 2)
			}
			t.Problems = append(t.Problems, p)
		}
		// La demo pausa el SLA mientras espera al solicitante (las plantillas no traen estados de pausa).
		// Se agrega después de armar los incidentes para no cambiar los de siempre.
		st := t.Config.States
		t.Config.States = append(append(append([]string{}, st[:len(st)-1]...), service.WaitingRequester), final)
		t.Config.PauseStates = []string{service.WaitingRequester}
		lifecycle(t, life, now, byProblem, i == 0)
		out = append(out, t)
	}
	return out
}

// Textos de la demo, genéricos para cualquier rubro.
var (
	notes = []string{
		"Revisé los registros: el error se repite desde temprano.",
		"Lo reproduje en otro puesto: no es algo puntual de este usuario.",
		"Hablé con el proveedor, quedaron en revisarlo.",
		"Parece relacionado con otros reportes del mismo servicio.",
	}
	replies = []string{
		"Hola, ya lo estamos revisando. Te avisamos apenas tengamos novedades.",
		"Gracias por avisar: estamos trabajando en esto.",
		"¿Nos confirmás desde qué hora pasa y si le pasa a alguien más?",
	}
	reporterReplies = []string{
		"Gracias, quedo atento.",
		"Pasa desde las 9 y a mis compañeros también.",
		"Sigue pasando, ¿hay novedades?",
	}
	solved = []string{
		"Se reinició el servicio y quedó funcionando.",
		"Se corrigió la configuración; el usuario confirmó que anda.",
		"Se reemplazó el equipo afectado.",
	}
)

// lifecycle arma el historial de cada incidente de demo hasta la etapa que ya tiene (registro, diagnóstico,
// respuestas, notas, resolución y cierre), con los mismos efectos que la API: primera respuesta, pausas del SLA
// (corren el vencimiento de resolución) y cierre automático tras AutoCloseDays sin actividad. byProblem: incidentes que resolvió su problema, con el estado en el
// que estaban. Además deja tres esperando al solicitante (en pausa, uno todavía dentro del plazo), uno retenido por su solicitante después de
// resuelto, uno reabierto (again: se volvió a resolver; si no, quedó abierto) y uno relacionado con un cerrado.
func lifecycle(t *domain.Tenant, rnd *rand.Rand, now time.Time, byProblem map[int]string, again bool) {
	states, pause, final := t.Config.States, t.Config.PauseStates, t.ResolvedState()
	autoClose := time.Duration(t.Config.AutoCloseDays) * 24 * time.Hour
	solicitante := len(t.Users) // el último usuario de demo: solo crea tickets

	paused, discarded := 0, 0 // discarded: resueltos como duplicado o cancelado, que cierran al resolver (uno o dos)
	for _, inc := range t.Incidents {
		if paused < 2 && inc.ResolvedAt == nil && inc.ProblemID == 0 {
			inc.Status = service.WaitingRequester
			paused++
		}
	}
	// Y uno más en pausa todavía dentro del plazo de resolución: el primero abierto cuyo vencimiento cae después de
	// la mitad de su vida, que es antes de donde entra a la pausa (ver path).
	var onTime *domain.Incident
	for _, inc := range t.Incidents {
		mid := inc.CreatedAt.Add(now.Sub(inc.CreatedAt) / 2)
		if onTime == nil && inc.ResolvedAt == nil && inc.ProblemID == 0 && inc.Status != service.WaitingRequester && inc.DueAt.After(mid) {
			onTime = inc
		}
	}
	if onTime != nil {
		onTime.Status = service.WaitingRequester
	}
	var reopened, held *domain.Incident
	for _, inc := range t.Incidents {
		if r := inc.ResolvedAt; reopened == nil && r != nil && inc.ProblemID == 0 &&
			r.Before(now.AddDate(0, 0, -3)) && r.After(now.AddDate(0, 0, -20)) {
			reopened = inc
		}
	}
	for _, mine := range []bool{true, false} { // de preferencia uno que ya sea del solicitante
		for _, inc := range t.Incidents {
			if r := inc.ResolvedAt; held == nil && inc != reopened && r != nil && inc.ProblemID == 0 &&
				r.After(now.AddDate(0, 0, -6)) && r.Before(now.Add(-6*time.Hour)) && (!mine || inc.CreatedBy == solicitante) {
				held, inc.CreatedBy = inc, solicitante
			}
		}
	}

	for _, inc := range t.Incidents {
		target, end := inc.Status, now
		if inc.ResolvedAt != nil {
			end = *inc.ResolvedAt
		}
		from, closedByProblem := byProblem[inc.ID]
		c, span := inc.CreatedAt, end.Sub(inc.CreatedAt)
		f := func(x float64) time.Time { return c.Add(time.Duration(x * float64(span))) }
		agent := inc.AssigneeID
		if agent == 0 {
			agent = 2 + rnd.Intn(2)
		}

		// Se rearma desde el registro: estado inicial, sin resolución.
		inc.Status, inc.ResolvedAt, inc.History = states[0], nil, []domain.Event{}
		if t.Config.Modules.SLA {
			resp := c.Add(time.Duration(t.Config.ResponseHours[inc.Priority]) * time.Hour)
			inc.ResponseDueAt = &resp
		}
		s := story{t, inc}
		var steps []step
		add := func(at time.Time, do func(at time.Time)) { steps = append(steps, step{at, do}) }

		add(c, func(at time.Time) { inc.Log(at, inc.CreatedBy, domain.EvCreated, "Incidente registrado") })
		if inc.AssigneeID != 0 {
			name := t.User(inc.AssigneeID).Name
			add(f(0.03), func(at time.Time) { inc.Log(at, 2, domain.EvAssigned, "Asignado a %s", name) })
		}
		// Los anteriores al problema se vinculan al crearlo (aunque ya estén cerrados); los posteriores, al registrarlos.
		var p *domain.Problem
		if inc.ProblemID != 0 {
			p = t.Problems[inc.ProblemID-1]
			linkAt := c.Add(10 * time.Minute)
			if linkAt.Before(p.CreatedAt) {
				linkAt = p.CreatedAt
			}
			add(linkAt, func(at time.Time) { inc.Log(at, 2, domain.EvProblem, "Vinculado al problema P-%d", p.ID) })
		}

		// Diagnóstico: los estados intermedios hasta el actual (o el de antes de la resolución), sin pasar por
		// los de pausa salvo que sea donde está. Algunos se resuelven directo desde el estado inicial.
		last, stay := slices.Index(states, target), target
		switch {
		case closedByProblem:
			last, stay = slices.Index(states, from), from
		case target == final && rnd.Float64() < 0.2:
			last = 0
		case target == final:
			last = len(states) - 2
		}
		path := []string{}
		for _, st := range states[1 : last+1] {
			if st == stay || !domain.Has(pause, st) {
				path = append(path, st)
			}
		}
		for k, st := range path {
			add(f(0.15+0.35*float64(k)/float64(len(path))), func(at time.Time) { s.move(at, agent, st) })
		}
		// La primera respuesta es la primera respuesta pública de quien atiende (cambiar el estado no cuenta).
		// onTime hace el sorteo como si no estuviera en pausa, para no cambiar el resto de la demo.
		if (target == service.WaitingRequester && inc != onTime) || rnd.Float64() < 0.8 || inc == onTime {
			text := replies[rnd.Intn(len(replies))]
			add(f(0.1+0.5*rnd.Float64()), func(at time.Time) { s.reply(at, agent, text) })
		}
		if len(path) > 0 && rnd.Float64() < 0.7 {
			text := notes[rnd.Intn(len(notes))]
			add(f(0.25+0.5*rnd.Float64()), func(at time.Time) { inc.Log(at, agent, domain.EvNote, "%s", text) })
		}
		if inc.CreatedBy != agent && rnd.Float64() < 0.35 {
			text := reporterReplies[rnd.Intn(len(reporterReplies))]
			add(f(0.3+0.6*rnd.Float64()), func(at time.Time) { s.reply(at, inc.CreatedBy, text) })
		}

		// Resolución y cierre: duplicado y cancelado cierran al resolver; del resto, algunos los cierra quien
		// atiende y los demás se cierran solos (ver AutoClose al final) o siguen resueltos.
		if target == final {
			by, code, note := agent, "solucionado", solved[rnd.Intn(len(solved))]
			switch r := rnd.Float64(); {
			case closedByProblem:
				by, note = 2, p.Solution // como en la API: la nota es solo la solución
			case inc == reopened || inc == held:
			case p != nil && p.Workaround != "" && r < 0.5:
				code, note = "workaround", fmt.Sprintf("Se aplicó el workaround del problema P-%d: %s", p.ID, p.Workaround)
			case r < 0.75:
			case r < 0.85 && discarded < 1:
				code, note = "duplicado", "Duplicado de otro incidente ya registrado."
				discarded++
			case r < 0.92:
				code, note = "sin_reproducir", "No se pudo reproducir; el usuario confirmó que ya funciona."
			case discarded < 2:
				code, note = "cancelado", "Quien lo reportó avisó que ya no hace falta."
				discarded++
			}
			add(end, func(at time.Time) { s.resolve(at, by, code, note) })
			closeAt := end.Add(time.Duration((0.1 + 2.9*rnd.Float64()) * 24 * float64(time.Hour)))
			if !closedByProblem && inc != reopened && inc != held && !domain.Has(domain.ClosingCodes, code) &&
				rnd.Float64() < 0.4 && closeAt.Before(now) && closeAt.Before(end.Add(autoClose)) {
				add(closeAt, func(at time.Time) { s.close(at, agent) })
			}
		}

		// Retenido: su solicitante respondió estando resuelto, así que ya no se cierra solo.
		if inc == held {
			add(end.Add(2*time.Hour), func(at time.Time) { s.reply(at, inc.CreatedBy, "Sigue fallando de a ratos, ¿lo pueden mirar?") })
		}
		// Reapertura: quien lo reportó avisa que volvió a pasar y quien atiende lo reabre, antes del cierre.
		if inc == reopened {
			add(end.Add(5*time.Hour), func(at time.Time) { s.reply(at, inc.CreatedBy, "Volvió a pasar.") })
			add(end.Add(6*time.Hour), func(at time.Time) { s.reopen(at, agent, "Quien lo reportó avisó que volvió a pasar.") })
			add(end.Add(8*time.Hour), func(at time.Time) { s.reply(at, agent, "Lo retomamos, gracias por avisar.") })
			if again {
				add(end.Add(10*time.Hour), func(at time.Time) { s.move(at, agent, states[1]) })
				add(end.Add(30*time.Hour), func(at time.Time) {
					s.resolve(at, agent, "solucionado", "Había una segunda causa: se corrigió y el usuario confirmó que anda.")
				})
			}
		}

		// Como en la API, el barrido de cierre automático corre antes de cada paso y una vez más al final.
		sort.SliceStable(steps, func(a, b int) bool { return steps[a].at.Before(steps[b].at) })
		for _, st := range steps {
			inc.AutoClose(t.Config.AutoCloseDays, st.at)
			st.do(st.at)
		}
		inc.AutoClose(t.Config.AutoCloseDays, now)
	}

	// Un incidente abierto que continúa uno cerrado del mismo servicio (de quien lo reporta, si solo reporta).
	for _, inc := range t.Incidents {
		if inc.ResolvedAt != nil || inc.ProblemID != 0 || inc.ReopenCount > 0 {
			continue
		}
		for k := len(t.Incidents) - 1; k >= 0; k-- {
			old := t.Incidents[k]
			if old.Closed() && old.ClosedAt.Before(inc.CreatedAt) && old.Service == inc.Service &&
				(t.Can(inc.CreatedBy, domain.PermResolve) || old.CreatedBy == inc.CreatedBy) {
				inc.RelatedIncidentID = old.ID
				inc.History[0].Text = fmt.Sprintf("Incidente registrado (relacionado con #%d)", old.ID)
				old.Log(inc.CreatedAt, inc.CreatedBy, domain.EvRelated, "Se registró #%d relacionado", inc.ID)
				return
			}
		}
	}
}

type step struct {
	at time.Time
	do func(at time.Time)
}

// story aplica a un incidente de demo los mismos pasos que la API (ver service/incidents.go), con fechas en el pasado.
type story struct {
	t   *domain.Tenant
	inc *domain.Incident
}

func (s story) move(at time.Time, by int, to string) {
	s.inc.Log(at, by, domain.EvStatus, "De %s a %s", s.inc.Status, to)
	s.inc.Status = to
	s.inc.SyncPause(s.t.Config.PauseStates, at)
}

func (s story) reply(at time.Time, by int, text string) {
	switch {
	case s.t.Can(by, domain.PermResolve):
		s.inc.Respond(at)
	case s.inc.Resolved():
		s.inc.AutoCloseHold = true
	}
	s.inc.Log(at, by, domain.EvReply, "%s", text)
}

func (s story) resolve(at time.Time, by int, code, note string) {
	from := s.inc.Status
	s.inc.Status = s.t.ResolvedState()
	s.inc.SyncPause(s.t.Config.PauseStates, at)
	s.inc.ResolvedAt, s.inc.ResolutionCode, s.inc.ResolutionNote = &at, code, note
	s.inc.Log(at, by, domain.EvResolved, "De %s a %s · %s: %s", from, s.inc.Status, domain.ResolutionLabels[code], note)
	if domain.Has(domain.ClosingCodes, code) {
		s.close(at, by)
	}
}

func (s story) close(at time.Time, by int) {
	s.inc.ClosedAt = &at
	s.inc.Log(at, by, domain.EvClosed, "Cerrado por %s", s.t.User(by).Name)
}

func (s story) reopen(at time.Time, by int, reason string) {
	from := s.inc.Status
	s.inc.Status = s.t.Config.States[0]
	s.inc.SyncPause(s.t.Config.PauseStates, at)
	s.inc.ResolvedAt, s.inc.ResolutionCode, s.inc.ResolutionNote = nil, "", ""
	s.inc.ReopenCount++
	s.inc.AutoCloseHold = false
	s.inc.Log(at, by, domain.EvReopened, "De %s a %s: %s", from, s.inc.Status, reason)
}
