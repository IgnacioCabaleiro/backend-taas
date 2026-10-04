package main

import (
	"math/rand"
	"sort"
	"time"
)

// Plantillas por rubro: el punto de partida de cada empresa nueva, editable después desde Configuración.
type template struct {
	Industry string
	Config   Config
}

// Setup: lo que el titular responde en el onboarding. No elige "módulos": cuenta cómo trabaja
// y de ahí sale la configuración inicial, que después puede ajustar desde Configuración.
type Setup struct {
	Template string   `json:"template"`
	Industry string   `json:"industry"` // solo para el rubro "otro": cómo se describe el cliente
	Services []string `json:"services"` // el catálogo sugerido, ya ajustado por el cliente
	Team     bool     `json:"team"`     // ¿atiende más de una persona? => flujo de estados completo
	SLA      bool     `json:"sla"`      // ¿hay plazos que cumplir? => vencimientos por prioridad
	Problems bool     `json:"problems"` // ¿se repiten los mismos incidentes? => gestión de problemas
	Fields   bool     `json:"fields"`   // ¿hace falta pedir datos propios en cada ticket?
}

// full: la plantilla con todo activado (cuentas de demo, vista previa del onboarding).
var full = Setup{Team: true, SLA: true, Problems: true, Fields: true}

// build arma la configuración para unas respuestas. Devuelve una copia: cada cuenta edita la suya
// sin tocar la plantilla.
func (t template) build(in Setup) (Config, error) {
	c := t.Config
	c.Modules = Modules{SLA: in.SLA, Problems: in.Problems}
	c.Services = append([]string{}, c.Services...)
	if len(in.Services) > 0 {
		c.Services = in.Services
	}
	c.States = append([]string{}, c.States...)
	if !in.Team {
		c.States = []string{c.States[0], c.States[len(c.States)-1]} // sin pasos intermedios: abrir y cerrar
	}
	c.SLAHours = map[string]int{}
	for k, v := range t.Config.SLAHours {
		c.SLAHours[k] = v
	}
	c.Fields = []Field{}
	if in.Fields {
		for _, f := range t.Config.Fields {
			f.Key = slug(f.Label)
			f.Options = append([]string{}, f.Options...)
			c.Fields = append(c.Fields, f)
		}
	}
	return c, c.validate()
}

var templateOrder = []string{"it", "salud", "logistica", "otro"}

var templates = map[string]template{
	"it": {"Soporte IT", Config{
		Services: []string{"VPN", "Correo", "ERP Facturación", "Impresoras", "WiFi"},
		States:   []string{"Abierto", "En curso", "Resuelto"},
		SLAHours: map[string]int{"alta": 4, "media": 8, "baja": 24},
		Fields: []Field{
			{Label: "Sede", Type: "select", Options: []string{"Casa central", "Sucursal Rosario", "Remoto"}, Required: true},
			{Label: "Equipo afectado", Type: "text"},
		},
		RecurrenceMin: 2, RecurrenceDays: 7,
	}},
	"salud": {"Salud", Config{
		Services: []string{"Historia clínica electrónica", "Turnos online", "Equipamiento de imágenes", "Laboratorio", "Facturación a obras sociales"},
		States:   []string{"Reportado", "En revisión", "Derivado a proveedor", "Resuelto"},
		SLAHours: map[string]int{"alta": 1, "media": 4, "baja": 12},
		Fields: []Field{
			{Label: "Sector", Type: "select", Options: []string{"Guardia", "Internación", "Consultorios", "Diagnóstico"}, Required: true},
			{Label: "Afecta la atención de pacientes", Type: "select", Options: []string{"Sí", "No"}, Required: true},
			{Label: "N° de equipo", Type: "text"},
		},
		RecurrenceMin: 2, RecurrenceDays: 14,
	}},
	"logistica": {"Logística", Config{
		Services: []string{"Sistema de ruteo", "Handhelds de depósito", "Tracking de envíos", "Balanza y etiquetado", "Facturación"},
		States:   []string{"Nuevo", "Asignado", "En espera de repuesto", "Cerrado"},
		SLAHours: map[string]int{"alta": 2, "media": 8, "baja": 48},
		Fields: []Field{
			{Label: "Depósito", Type: "select", Options: []string{"Pilar", "Avellaneda", "Córdoba"}, Required: true},
			{Label: "Patente o unidad", Type: "text"},
			{Label: "Envíos afectados", Type: "number"},
		},
		RecurrenceMin: 3, RecurrenceDays: 7,
	}},
	// Punto de partida genérico: el cliente describe su rubro y ajusta los servicios en el onboarding.
	"otro": {"Otro rubro", Config{
		Services: []string{"Atención al cliente", "Sistemas", "Administración"},
		States:   []string{"Nuevo", "En curso", "Resuelto"},
		SLAHours: map[string]int{"alta": 4, "media": 8, "baja": 24},
		Fields: []Field{
			{Label: "Área", Type: "select", Options: []string{"Administración", "Ventas", "Operaciones"}, Required: true},
			{Label: "Contacto", Type: "text"},
		},
		RecurrenceMin: 2, RecurrenceDays: 7,
	}},
}

// --- Empresas de demo ---

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

// Contraseña de todos los usuarios de demo (datos de prueba; ver README).
const demoPassword = "demo-taas-2026"

// seedEmail: "Ana Gómez" en nexo.demo -> ana@nexo.demo
func seedEmail(name, domain string) string {
	first := []rune{}
	for _, r := range name {
		if r == ' ' {
			break
		}
		first = append(first, r)
	}
	ascii := map[rune]rune{'á': 'a', 'é': 'e', 'í': 'i', 'ó': 'o', 'ú': 'u'}
	out := []rune{}
	for _, r := range []rune(slug(string(first))) {
		if a, ok := ascii[r]; ok {
			r = a
		}
		out = append(out, r)
	}
	return string(out) + "@" + domain
}

func seed() []*Tenant {
	rnd := rand.New(rand.NewSource(7)) // fijo: la demo arranca siempre igual
	now := time.Now()
	hash := hashPassword(demoPassword) // una sola vez: derivarla es caro a propósito
	out := []*Tenant{}
	for i, sd := range seeds {
		tpl := templates[sd.template]
		cfg, _ := tpl.build(full)
		t := &Tenant{ID: i + 1, Name: sd.name, Industry: tpl.Industry, Onboarded: true, OwnerID: 1, Config: cfg,
			Roles: defaultRoles(), NextUserID: len(sd.users) + 1, NextRoleID: 4, Problems: []*Problem{}}
		for j, name := range sd.users {
			role := []int{1, 2, 2, 3}[j] // titular, agente, agente, solicitante
			t.Users = append(t.Users, User{j + 1, name, seedEmail(name, sd.domain), role, hash})
		}
		closed := t.closedState()

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
			prio := priorities[[]int{0, 1, 1, 2}[rnd.Intn(4)]]
			created := now.Add(-time.Duration(age * float64(time.Hour)))
			sla := time.Duration(t.Config.SLAHours[prio]) * time.Hour
			due := created.Add(sla)
			inc := &Incident{ID: len(t.Incidents) + 1, Title: sd.titles[service][rnd.Intn(len(sd.titles[service]))],
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
				inc.Status, inc.ResolvedAt = closed, &done
			}
			t.Incidents = append(t.Incidents, inc)
		}

		for _, sp := range sd.problems {
			p := &Problem{ID: len(t.Problems) + 1, Title: sp.title, Description: sp.desc, Service: sp.service, OwnerID: 2}
			var linked []*Incident
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
				p.History = append(p.History, Event{at, kind, text, by})
				at = at.Add(26 * time.Hour)
			}
			p.CreatedAt, p.Status = at, Identificado
			step("", "Problema creado desde 2 incidentes recurrentes", 1)
			if sp.stage >= 1 {
				p.Status = EnAnalisis
				step("analisis", "Análisis de causa raíz iniciado", 2)
			}
			if sp.stage >= 2 {
				p.Status, p.RootCause, p.Workaround = ErrorConocido, sp.rootCause, sp.wa
				step("conocido", "Registrado como error conocido", 2)
			}
			if sp.stage == 3 {
				p.Status, p.Solution = Resuelto, sp.sol
				at = now.Add(-20 * time.Hour)
				for _, inc := range linked {
					if inc.ResolvedAt == nil {
						done := inc.DueAt.Add(2 * time.Hour)
						if done.After(at) {
							done = at
						}
						inc.Status, inc.ResolvedAt = closed, &done
					}
				}
				step("resuelto", "Resuelto · incidentes vinculados cerrados", 2)
			}
			t.Problems = append(t.Problems, p)
		}
		out = append(out, t)
	}
	return out
}
