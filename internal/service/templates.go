package service

import "taas-backend/internal/domain"

// Plantillas por rubro: el punto de partida de cada cuenta nueva, editable después desde Configuración.

var templateOrder = []string{"it", "salud", "logistica", "otro"}

var templates = map[string]Template{
	"it": {Industry: "Soporte IT", Config: domain.Config{
		Services: []string{"VPN", "Correo", "ERP Facturación", "Impresoras", "WiFi"},
		States:   []string{"Abierto", "En curso", "Resuelto"},
		SLAHours: map[string]int{"alta": 4, "media": 8, "baja": 24},
		Fields: []domain.Field{
			{Label: "Sede", Type: "select", Options: []string{"Casa central", "Sucursal Rosario", "Remoto"}, Required: true},
			{Label: "Equipo afectado", Type: "text"},
		},
		RecurrenceMin: 2, RecurrenceDays: 7,
	}},
	"salud": {Industry: "Salud", Config: domain.Config{
		Services: []string{"Historia clínica electrónica", "Turnos online", "Equipamiento de imágenes", "Laboratorio", "Facturación a obras sociales"},
		States:   []string{"Reportado", "En revisión", "Derivado a proveedor", "Resuelto"},
		SLAHours: map[string]int{"alta": 1, "media": 4, "baja": 12},
		Fields: []domain.Field{
			{Label: "Sector", Type: "select", Options: []string{"Guardia", "Internación", "Consultorios", "Diagnóstico"}, Required: true},
			{Label: "Afecta la atención de pacientes", Type: "select", Options: []string{"Sí", "No"}, Required: true},
			{Label: "N° de equipo", Type: "text"},
		},
		RecurrenceMin: 2, RecurrenceDays: 14,
	}},
	"logistica": {Industry: "Logística", Config: domain.Config{
		Services: []string{"Sistema de ruteo", "Handhelds de depósito", "Tracking de envíos", "Balanza y etiquetado", "Facturación"},
		States:   []string{"Nuevo", "Asignado", "En espera de repuesto", "Cerrado"},
		SLAHours: map[string]int{"alta": 2, "media": 8, "baja": 48},
		Fields: []domain.Field{
			{Label: "Depósito", Type: "select", Options: []string{"Pilar", "Avellaneda", "Córdoba"}, Required: true},
			{Label: "Patente o unidad", Type: "text"},
			{Label: "Envíos afectados", Type: "number"},
		},
		RecurrenceMin: 3, RecurrenceDays: 7,
	}},
	// Punto de partida genérico: el cliente describe su rubro y ajusta los servicios en el onboarding.
	"otro": {Industry: "Otro rubro", Config: domain.Config{
		Services: []string{"Atención al cliente", "Sistemas", "Administración"},
		States:   []string{"Nuevo", "En curso", "Resuelto"},
		SLAHours: map[string]int{"alta": 4, "media": 8, "baja": 24},
		Fields: []domain.Field{
			{Label: "Área", Type: "select", Options: []string{"Administración", "Ventas", "Operaciones"}, Required: true},
			{Label: "Contacto", Type: "text"},
		},
		RecurrenceMin: 2, RecurrenceDays: 7,
	}},
}
