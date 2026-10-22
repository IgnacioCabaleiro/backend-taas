package service

import "taas-backend/internal/domain"

// Plantillas por rubro: el punto de partida de cada cuenta nueva, editable después desde Configuración.
// Las horas de primera respuesta son las mismas que las del SLA de resolución. Ninguna plantilla trae
// estados de pausa: cada cuenta los suma en el onboarding (WaitsRequester, que agrega WaitingRequester o lo reusa
// si la plantilla ya lo trae) o los configura después.

var templateOrder = []string{"it", "salud", "logistica", "otro"}

var templates = map[string]Template{
	"it": {Industry: "Soporte IT", Config: domain.Config{
		Services:      []string{"VPN", "Correo", "ERP Facturación", "Impresoras", "WiFi"},
		States:        []string{"Nuevo", "En curso", "Resuelto"},
		SLAHours:      map[string]int{"alta": 4, "media": 8, "baja": 24},
		ResponseHours: map[string]int{"alta": 4, "media": 8, "baja": 24},
		PauseStates:   []string{},
		AutoCloseDays: domain.DefaultAutoCloseDays,
		Fields: []domain.Field{
			{Label: "Sede", Type: "select", Options: []string{"Casa central", "Sucursal Rosario", "Remoto"}, Required: true},
			{Label: "Equipo afectado", Type: "text"},
		},
		RecurrenceMin: 2, RecurrenceDays: 7,
	}},
	"salud": {Industry: "Salud", Config: domain.Config{
		Services:      []string{"Historia clínica electrónica", "Turnos online", "Equipamiento de imágenes", "Laboratorio", "Facturación a obras sociales"},
		States:        []string{"Reportado", "En revisión", "Derivado a proveedor", "Resuelto"},
		SLAHours:      map[string]int{"alta": 1, "media": 4, "baja": 12},
		ResponseHours: map[string]int{"alta": 1, "media": 4, "baja": 12},
		PauseStates:   []string{},
		AutoCloseDays: domain.DefaultAutoCloseDays,
		Fields: []domain.Field{
			{Label: "Sector", Type: "select", Options: []string{"Guardia", "Internación", "Consultorios", "Diagnóstico"}, Required: true},
			{Label: "Afecta la atención de pacientes", Type: "select", Options: []string{"Sí", "No"}, Required: true},
			{Label: "N° de equipo", Type: "text"},
		},
		RecurrenceMin: 2, RecurrenceDays: 14,
	}},
	"logistica": {Industry: "Logística", Config: domain.Config{
		Services:      []string{"Sistema de ruteo", "Handhelds de depósito", "Tracking de envíos", "Balanza y etiquetado", "Facturación"},
		States:        []string{"Nuevo", "Asignado", "En espera de repuesto", "Cerrado"},
		SLAHours:      map[string]int{"alta": 2, "media": 8, "baja": 48},
		ResponseHours: map[string]int{"alta": 2, "media": 8, "baja": 48},
		PauseStates:   []string{},
		AutoCloseDays: domain.DefaultAutoCloseDays,
		Fields: []domain.Field{
			{Label: "Depósito", Type: "select", Options: []string{"Pilar", "Avellaneda", "Córdoba"}, Required: true},
			{Label: "Patente o unidad", Type: "text"},
			{Label: "Envíos afectados", Type: "number"},
		},
		RecurrenceMin: 3, RecurrenceDays: 7,
	}},
	// Punto de partida genérico: el cliente describe su rubro y ajusta los servicios en el onboarding.
	"otro": {Industry: "Otro rubro", Config: domain.Config{
		Services:      []string{"Atención al cliente", "Sistemas", "Administración"},
		States:        []string{"Nuevo", "En curso", "Resuelto"},
		SLAHours:      map[string]int{"alta": 4, "media": 8, "baja": 24},
		ResponseHours: domain.DefaultResponseHours,
		PauseStates:   []string{},
		AutoCloseDays: domain.DefaultAutoCloseDays,
		Fields: []domain.Field{
			{Label: "Área", Type: "select", Options: []string{"Administración", "Ventas", "Operaciones"}, Required: true},
			{Label: "Contacto", Type: "text"},
		},
		RecurrenceMin: 2, RecurrenceDays: 7,
	}},
}
