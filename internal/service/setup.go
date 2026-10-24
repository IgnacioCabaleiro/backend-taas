package service

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"time"

	"taas-backend/internal/domain"
)

// Setup: onboarding y configuración de la cuenta.
type Setup struct{ base }

// Template: el punto de partida de un rubro (ver templates.go).
type Template struct {
	Key      string        `json:"key"`
	Industry string        `json:"industry"`
	Config   domain.Config `json:"config"`
}

// Full: las respuestas del onboarding en "sí" (cuentas de demo, vista previa), salvo WaitsRequester: las plantillas
// se muestran con su flujo tal cual (GET /api/templates) y las cuentas de demo suman ese estado por su cuenta (ver seed).
var Full = domain.Setup{Team: true, SLA: true, Problems: true, Fields: true}

// WaitingRequester: el estado que agrega el onboarding cuando hay que esperar a quien reportó. Pausa el SLA.
const WaitingRequester = "Esperando al solicitante"

// AddWaitingRequester deja WaitingRequester como único estado de pausa. Si el flujo no lo tiene, lo inserta justo
// antes del de resolución; si ya lo tiene, lo reusa donde está. No escribe sobre el arreglo de estados recibido.
func AddWaitingRequester(c *domain.Config) {
	if !domain.Has(c.States, WaitingRequester) {
		c.States = slices.Insert(slices.Clip(c.States), len(c.States)-1, WaitingRequester)
	}
	c.PauseStates = []string{WaitingRequester}
}

// build arma la configuración para unas respuestas. Devuelve una copia: cada cuenta edita la suya
// sin tocar la plantilla.
func (t Template) build(in domain.Setup) (domain.Config, error) {
	c := t.Config
	c.Modules = domain.Modules{SLA: in.SLA, Problems: in.Problems}
	c.Services = append([]string{}, c.Services...)
	if len(in.Services) > 0 {
		c.Services = in.Services
	}
	c.States = append([]string{}, c.States...)
	c.PauseStates = append([]string{}, c.PauseStates...)
	if !in.Team {
		// Sin pasos intermedios: abrir y cerrar. Tampoco quedan estados en los que pausar el SLA.
		c.States = []string{c.States[0], c.States[len(c.States)-1]}
		c.PauseStates = []string{}
	}
	if in.SLA && in.WaitsRequester {
		// Sin SLA no hay reloj que pausar: se ignora. Con equipo, si la plantilla ya trae el estado se reusa donde está;
		// si no, va justo antes del de resolución. Sin equipo el flujo ya se recortó a inicial y resolución (si la
		// plantilla lo traía, se fue con el recorte) y se vuelve a insertar antes del de resolución: inicial, esperando,
		// resolución.
		AddWaitingRequester(&c)
	}
	responseHours := t.Config.ResponseHours
	if in.SLA && in.ResponseHours.Set {
		// Sin SLA no se usan: quedan los de la plantilla. Un null o un 0 no cae en la plantilla: lo rechaza Validate.
		responseHours = in.ResponseHours.Value
	}
	// Copias: la cuenta no comparte mapas con la plantilla ni con el pedido. Validate deja solo las prioridades
	// (y un mapa no nil aunque la respuesta haya venido en null).
	c.SLAHours, c.ResponseHours = maps.Clone(t.Config.SLAHours), maps.Clone(responseHours)
	if in.AutoCloseDays.Set {
		c.AutoCloseDays = in.AutoCloseDays.Value // null queda en 0: lo rechaza Validate
	}
	c.Fields = []domain.Field{}
	if in.Fields {
		for _, f := range t.Config.Fields {
			f.Key = domain.Slug(f.Label)
			f.Options = append([]string{}, f.Options...)
			c.Fields = append(c.Fields, f)
		}
	}
	return c, c.Validate()
}

// BuildConfig arma la configuración de un rubro para unas respuestas.
func BuildConfig(template string, in domain.Setup) (industry string, c domain.Config, err error) {
	tpl, ok := templates[template]
	if !ok {
		return "", c, errors.New("elegí un rubro")
	}
	c, err = tpl.build(in)
	return tpl.Industry, c, err
}

// Templates lista los rubros disponibles, con todo activado, para el onboarding.
func (s *Setup) Templates() []Template {
	out := []Template{}
	for _, k := range templateOrder {
		c, _ := templates[k].build(Full)
		out = append(out, Template{k, templates[k].Industry, c})
	}
	return out
}

// Onboard aplica las respuestas del onboarding. Se puede repetir (el titular puede volver atrás)
// hasta que lo da por terminado con Finish.
func (s *Setup) Onboard(tenantID, by int, in domain.Setup) error {
	return s.update(tenantID, by, domain.PermConfig, func(t *domain.Tenant) error {
		if t.Onboarded {
			return errors.New("la cuenta ya está configurada: los ajustes se hacen desde Configuración")
		}
		industry, c, err := BuildConfig(in.Template, in)
		if err != nil {
			return err
		}
		if in.Template == "otro" {
			if industry = strings.TrimSpace(in.Industry); industry == "" {
				return errors.New("contanos a qué se dedica tu empresa")
			}
		}
		t.Industry, t.Config = industry, c
		return nil
	})
}

func (s *Setup) Finish(tenantID, by int) error {
	return s.update(tenantID, by, domain.PermConfig, func(t *domain.Tenant) error {
		if t.Industry == "" {
			return errors.New("falta configurar la ticketera")
		}
		t.Onboarded = true
		return nil
	})
}

func (s *Setup) SetConfig(tenantID, by int, c domain.Config) error {
	return s.update(tenantID, by, domain.PermConfig, func(t *domain.Tenant) error {
		// Los estados de pausa se validan después de renombrarlos: pueden venir con el nombre viejo de un estado renombrado.
		pause := c.PauseStates
		c.PauseStates = nil
		if err := c.Validate(); err != nil {
			return err
		}
		// Ítems renombrados en el lugar: los datos existentes siguen al nombre nuevo (ver renames).
		states, services := renames(t.Config.States, c.States), renames(t.Config.Services, c.Services)
		c.PauseStates = []string{}
		for _, p := range pause {
			p = strings.TrimSpace(p)
			if n, ok := states[p]; ok {
				p = n
			}
			c.PauseStates = append(c.PauseStates, p)
		}
		if err := c.ValidatePauseStates(); err != nil {
			return err
		}

		t.Config = c
		for _, p := range t.Problems {
			if n, ok := services[p.Service]; ok {
				p.Service = n
			}
		}
		// Incidentes que quedaron en un estado eliminado: vuelven al inicial (o al de resolución si ya estaban resueltos
		// o cerrados: el servicio y el estado de un cerrado se siguen migrando para que apunten a la configuración vigente).
		// Después se recalcula la pausa de los abiertos: se detiene o se reanuda según el estado en el que quedaron.
		now := time.Now()
		for _, inc := range t.Incidents {
			if n, ok := services[inc.Service]; ok {
				inc.Service = n
			}
			if n, ok := states[inc.Status]; ok {
				inc.Status = n
			}
			if inc.ResolvedAt != nil {
				inc.Status = t.ResolvedState()
				continue
			}
			if !domain.Has(c.States[:len(c.States)-1], inc.Status) {
				inc.Log(now, by, domain.EvStatus, "De %s a %s (se eliminó el estado %s)", inc.Status, c.States[0], inc.Status)
				inc.Status = c.States[0]
			}
			inc.SyncPause(c.PauseStates, now)
		}
		return nil
	})
}

// renames detecta los ítems renombrados en el lugar: con la misma cantidad de ítems, la posición
// cuyo nombre viejo ya no está y cuyo nombre nuevo no existía antes. Reordenar no es renombrar.
func renames(old, new []string) map[string]string {
	m := map[string]string{}
	if len(old) == len(new) {
		for i := range old {
			if old[i] != new[i] && !slices.Contains(new, old[i]) && !slices.Contains(old, new[i]) {
				m[old[i]] = new[i]
			}
		}
	}
	return m
}
