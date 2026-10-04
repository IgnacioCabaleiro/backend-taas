// Package seed arma las cuentas de demo con las que arranca una instalación vacía.
package seed

import (
	"math/rand"
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

// Tenants arma las cuentas de demo, con un mes de actividad y un problema en cada etapa.
func Tenants() []*domain.Tenant {
	rnd := rand.New(rand.NewSource(7)) // fijo: la demo arranca siempre igual
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
		closed := t.ClosedState()

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
				inc.Status, inc.ResolvedAt = closed, &done
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
			p.CreatedAt, p.Status = at, domain.Identificado
			step("", "Problema creado desde 2 incidentes recurrentes", 1)
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
