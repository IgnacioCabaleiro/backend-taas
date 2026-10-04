package domain

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

type Field struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"` // text | number | select
	Options  []string `json:"options"`
	Required bool     `json:"required"`
}

// Modules: qué tan compleja es la gestión de tickets del cliente. Sale de sus respuestas en el onboarding.
type Modules struct {
	SLA      bool `json:"sla"`      // vencimientos por prioridad
	Problems bool `json:"problems"` // gestión de problemas y errores conocidos
}

// Config: lo que cada cliente ajusta a su medida.
type Config struct {
	Modules        Modules        `json:"modules"`
	Services       []string       `json:"services"`
	States         []string       `json:"states"`   // estados del incidente, en orden; el último es el de cierre
	SLAHours       map[string]int `json:"slaHours"` // horas para resolver, por prioridad
	Fields         []Field        `json:"fields"`   // campos propios del incidente
	RecurrenceMin  int            `json:"recurrenceMin"`
	RecurrenceDays int            `json:"recurrenceDays"`
}

var Priorities = []string{"alta", "media", "baja"}

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

func Has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Slug: clave estable de un campo a partir de su nombre.
func Slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

// clean recorta, descarta vacíos y exige que no haya repetidos.
func clean(list []string, what string) ([]string, error) {
	out := []string{}
	for _, s := range list {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		if Has(out, s) {
			return nil, fmt.Errorf("%s repetido: %s", what, s)
		}
		out = append(out, s)
	}
	return out, nil
}

// Validate normaliza la configuración y verifica que con ella se pueda operar.
func (c *Config) Validate() error {
	var err error
	if c.Services, err = clean(c.Services, "servicio"); err != nil {
		return err
	}
	if len(c.Services) == 0 {
		return errors.New("definí al menos un servicio")
	}
	if c.States, err = clean(c.States, "estado"); err != nil {
		return err
	}
	if len(c.States) < 2 {
		return errors.New("hacen falta al menos dos estados: uno inicial y uno de cierre")
	}
	for _, p := range Priorities {
		if c.SLAHours[p] < 1 {
			return fmt.Errorf("el SLA de prioridad %s tiene que ser de al menos 1 hora", p)
		}
	}
	keys := []string{}
	for i := range c.Fields {
		f := &c.Fields[i]
		if f.Label = strings.TrimSpace(f.Label); f.Label == "" {
			return errors.New("hay un campo sin nombre")
		}
		if f.Key == "" {
			f.Key = Slug(f.Label)
		}
		if Has(keys, f.Key) {
			return fmt.Errorf("campo repetido: %s", f.Label)
		}
		keys = append(keys, f.Key)
		switch f.Type {
		case "text", "number":
			f.Options = []string{}
		case "select":
			if f.Options, err = clean(f.Options, "opción"); err != nil {
				return err
			}
			if len(f.Options) < 2 {
				return fmt.Errorf("el campo %s necesita al menos dos opciones", f.Label)
			}
		default:
			return fmt.Errorf("tipo de campo inválido en %s", f.Label)
		}
	}
	if c.Fields == nil {
		c.Fields = []Field{}
	}
	if c.RecurrenceMin < 2 || c.RecurrenceDays < 1 {
		return errors.New("la regla de recurrencia necesita al menos 2 incidentes y 1 día")
	}
	return nil
}
