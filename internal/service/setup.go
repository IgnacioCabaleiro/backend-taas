package service

import (
	"errors"
	"strings"

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

// Full: todas las respuestas del onboarding en "sí" (cuentas de demo, vista previa).
var Full = domain.Setup{Team: true, SLA: true, Problems: true, Fields: true}

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
	if !in.Team {
		c.States = []string{c.States[0], c.States[len(c.States)-1]} // sin pasos intermedios: abrir y cerrar
	}
	c.SLAHours = map[string]int{}
	for k, v := range t.Config.SLAHours {
		c.SLAHours[k] = v
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
		if err := c.Validate(); err != nil {
			return err
		}
		// Misma cantidad de ítems que antes = se renombraron en el lugar: los datos existentes siguen al nombre nuevo.
		states, services := renames(t.Config.States, c.States), renames(t.Config.Services, c.Services)
		t.Config = c
		for _, p := range t.Problems {
			if n, ok := services[p.Service]; ok {
				p.Service = n
			}
		}
		// Incidentes que quedaron en un estado eliminado: vuelven al inicial (o al de cierre si ya estaban resueltos).
		for _, inc := range t.Incidents {
			if n, ok := services[inc.Service]; ok {
				inc.Service = n
			}
			if n, ok := states[inc.Status]; ok {
				inc.Status = n
			}
			if inc.ResolvedAt != nil {
				inc.Status = t.ClosedState()
			} else if !domain.Has(c.States[:len(c.States)-1], inc.Status) {
				inc.Status = c.States[0]
			}
		}
		return nil
	})
}

func renames(old, new []string) map[string]string {
	m := map[string]string{}
	if len(old) == len(new) {
		for i := range old {
			m[old[i]] = new[i]
		}
	}
	return m
}
