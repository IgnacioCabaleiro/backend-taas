package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
	States         []string       `json:"states"`   // flujo del incidente, en orden; el último es el de resolución
	SLAHours       map[string]int `json:"slaHours"` // horas para resolver, por prioridad
	Fields         []Field        `json:"fields"`   // campos propios del incidente
	RecurrenceMin  int            `json:"recurrenceMin"`
	RecurrenceDays int            `json:"recurrenceDays"`
	ResponseHours  map[string]int `json:"responseHours"` // horas para la primera respuesta, por prioridad
	// Estados en los que el reloj del SLA de resolución se detiene (por ejemplo, esperando al solicitante).
	// Nunca el inicial ni el de resolución: el reloj corre desde el registro hasta la resolución.
	PauseStates   []string `json:"pauseStates"`
	AutoCloseDays int      `json:"autoCloseDays"` // días sin actividad tras los que un resuelto se cierra solo
	// Catálogo de solicitudes de servicio: lo que se puede pedir y en cuánto se cumple. Vacío = la cuenta no
	// recibe solicitudes.
	RequestTypes []RequestType `json:"requestTypes"`
}

// RequestType: un ítem del catálogo de solicitudes. Hours es el plazo para cumplirla (el SLA de la solicitud,
// si el módulo está activo): no depende de una prioridad, porque un pedido estándar tiene un plazo estándar.
type RequestType struct {
	Name  string `json:"name"`
	Hours int    `json:"hours"`
}

// RequestType devuelve el ítem del catálogo con ese nombre, o nil.
func (c *Config) RequestType(name string) *RequestType {
	for i := range c.RequestTypes {
		if c.RequestTypes[i].Name == name {
			return &c.RequestTypes[i]
		}
	}
	return nil
}

// RequestTypeNames: los nombres del catálogo, en orden.
func (c *Config) RequestTypeNames() []string {
	out := []string{}
	for _, r := range c.RequestTypes {
		out = append(out, r.Name)
	}
	return out
}

// DefaultAutoCloseDays: el de todas las plantillas y el de las cuentas guardadas antes de que existiera el cierre.
const DefaultAutoCloseDays = 2

var Priorities = []string{"alta", "media", "baja"}

// Matriz de prioridad (ITIL 4): la prioridad no se elige, sale del impacto (a cuántos afecta) y de la urgencia
// (cuánto puede esperar). Los dos usan los mismos niveles que la prioridad. Sumando los niveles
// (alta = 0, media = 1, baja = 2): 0 o 1 es alta, 2 es media, 3 o 4 es baja.
//
//	impacto \ urgencia   alta    media   baja
//	alta                 alta    alta    media
//	media                alta    media   baja
//	baja                 media   baja    baja
//
// PriorityFor devuelve false si alguno de los dos no es un nivel válido.
func PriorityFor(impact, urgency string) (string, bool) {
	i, u := slices.Index(Priorities, impact), slices.Index(Priorities, urgency)
	if i < 0 || u < 0 {
		return "", false
	}
	switch s := i + u; {
	case s <= 1:
		return "alta", true
	case s == 2:
		return "media", true
	default:
		return "baja", true
	}
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
	// ¿Arrancar con los errores conocidos típicos del rubro? Solo si también hay gestión de problemas.
	KnownErrors bool `json:"knownErrors"`
	// ¿A veces hay que esperar datos o una confirmación de quien reportó? => estado "Esperando al solicitante",
	// que pausa el SLA de resolución. Solo con SLA (sin SLA se ignora); también sin equipo.
	WaitsRequester bool `json:"waitsRequester"`
	// Opcionales: si no vienen, quedan los de la plantilla. Si vienen en null o 0, los rechaza Config.Validate.
	ResponseHours Opt[map[string]int] `json:"responseHours,omitzero"` // horas de primera respuesta; solo con SLA
	AutoCloseDays Opt[int]            `json:"autoCloseDays,omitzero"` // días sin actividad para el cierre automático
}

// Opt: un campo opcional que distingue "no vino" (Set en false) de "vino", aunque sea null: un puntero no alcanza,
// porque encoding/json deja en nil tanto el campo ausente como el null. Un null queda con Set y el valor cero.
type Opt[T any] struct {
	Set   bool
	Value T
}

// UnmarshalJSON solo se llama si el campo vino; encoding/json la llama también con null.
func (o *Opt[T]) UnmarshalJSON(b []byte) error {
	var v T
	if string(b) != "null" {
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
	}
	*o = Opt[T]{true, v}
	return nil
}

// MarshalJSON es la vuelta: el valor tal cual (con omitzero, el que no vino no se escribe).
func (o Opt[T]) MarshalJSON() ([]byte, error) {
	if !o.Set {
		return []byte("null"), nil
	}
	return json.Marshal(o.Value)
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

// byPriority devuelve un mapa nuevo (nunca nil) solo con las claves que son prioridades: las demás se descartan.
// No toca el original, que puede estar compartido.
func byPriority(m map[string]int) map[string]int {
	out := map[string]int{}
	for _, p := range Priorities {
		if v, ok := m[p]; ok {
			out[p] = v
		}
	}
	return out
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
		return errors.New("hacen falta al menos dos estados: uno inicial y uno de resolución")
	}
	c.SLAHours, c.ResponseHours = byPriority(c.SLAHours), byPriority(c.ResponseHours)
	for _, p := range Priorities {
		if c.SLAHours[p] < 1 {
			return fmt.Errorf("el SLA de prioridad %s tiene que ser de al menos 1 hora", p)
		}
		if c.ResponseHours[p] < 1 {
			return fmt.Errorf("la primera respuesta de prioridad %s tiene que ser de al menos 1 hora", p)
		}
		// Primero se responde y después se resuelve (ITIL 4): igual está permitido. Vale aunque el SLA esté apagado,
		// como los otros chequeos de horas.
		if c.ResponseHours[p] > c.SLAHours[p] {
			return fmt.Errorf("la primera respuesta de prioridad %s (%d h) no puede superar a la resolución (%d h)",
				p, c.ResponseHours[p], c.SLAHours[p])
		}
	}
	if c.AutoCloseDays < 1 {
		return errors.New("el cierre automático necesita al menos 1 día")
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
	types := []RequestType{}
	for _, r := range c.RequestTypes {
		if r.Name = strings.TrimSpace(r.Name); r.Name == "" {
			return errors.New("hay una solicitud del catálogo sin nombre")
		}
		for _, o := range types {
			if o.Name == r.Name {
				return fmt.Errorf("solicitud repetida en el catálogo: %s", r.Name)
			}
		}
		if r.Hours < 1 {
			return fmt.Errorf("la solicitud %s necesita un plazo de al menos 1 hora", r.Name)
		}
		types = append(types, r)
	}
	c.RequestTypes = types
	return c.ValidatePauseStates()
}

// ValidatePauseStates normaliza los estados de pausa y verifica que estén en el flujo, sin ser el inicial
// ni el de resolución. Requiere los estados ya validados (Validate la llama al final).
func (c *Config) ValidatePauseStates() error {
	var err error
	if c.PauseStates, err = clean(c.PauseStates, "estado de pausa"); err != nil {
		return err
	}
	for _, s := range c.PauseStates {
		if !Has(c.States, s) {
			return fmt.Errorf("el estado de pausa %s no está en el flujo", s)
		}
		if s == c.States[0] || s == c.States[len(c.States)-1] {
			return fmt.Errorf("%s no puede pausar el SLA: el reloj corre desde el registro hasta la resolución", s)
		}
	}
	return nil
}
