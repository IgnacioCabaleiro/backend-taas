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
	Key         string        `json:"key"`
	Industry    string        `json:"industry"`
	Config      domain.Config `json:"config"`
	KnownErrors []KnownError  `json:"knownErrors"`
}

// KnownError: un error conocido típico de un rubro, para que la ticketera arranque sabiendo cosas.
type KnownError struct {
	Service    string `json:"service"`
	Title      string `json:"title"`
	RootCause  string `json:"rootCause"`
	Workaround string `json:"workaround"`
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
	// El catálogo de solicitudes viene de la plantilla del rubro (copia: no se comparte con ella).
	c.RequestTypes = append([]domain.RequestType{}, t.Config.RequestTypes...)
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
		out = append(out, Template{k, templates[k].Industry, c, templates[k].KnownErrors})
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
		preload(t, templates[in.Template].KnownErrors, in.KnownErrors && in.Problems, by)
		return nil
	})
}

// preload deja los errores conocidos típicos del rubro (solo los de servicios que siguen en el catálogo).
// El onboarding se puede repetir: los precargados de una pasada anterior se reemplazan. Durante el onboarding
// no hay otros problemas, así que los IDs siguen siendo correlativos.
func preload(t *domain.Tenant, known []KnownError, on bool, by int) {
	kept := []*domain.Problem{}
	for _, p := range t.Problems {
		if !p.Preloaded {
			kept = append(kept, p)
		}
	}
	if len(kept) != len(t.Problems) && len(kept) > 0 {
		return // ya hay problemas propios: no se toca nada
	}
	t.Problems = kept
	if !on {
		return
	}
	now := time.Now()
	for _, k := range known {
		if !domain.Has(t.Config.Services, k.Service) {
			continue
		}
		p := &domain.Problem{ID: len(t.Problems) + 1, Title: k.Title, Service: k.Service, Status: domain.ErrorConocido, OwnerID: by,
			RootCause: k.RootCause, Workaround: k.Workaround, CreatedAt: now, Preloaded: true, WorkaroundUses: []domain.WorkaroundUse{}}
		p.Log(by, "", "Precargado como error conocido típico del rubro")
		t.Problems = append(t.Problems, p)
	}
}

func (s *Setup) Finish(tenantID, by int) error {
	return s.update(tenantID, by, domain.PermConfig, func(t *domain.Tenant) error {
		if t.Industry == "" {
			return errors.New("falta configurar la ticketera")
		}
		t.Onboarded = true
		t.EnsureFirstVersion(time.Now(), by)
		return nil
	})
}

func (s *Setup) SetConfig(tenantID, by int, c domain.Config) error {
	return s.update(tenantID, by, domain.PermConfig, func(t *domain.Tenant) error {
		return applyConfig(t, by, c, 0)
	})
}

// RestoreVersion vuelve a una versión anterior: no borra historial, crea una versión nueva con ese contenido.
// Pasa por el mismo camino que guardar, así los tickets existentes se migran igual que en cualquier otro cambio.
func (s *Setup) RestoreVersion(tenantID, by, versionID int) error {
	return s.update(tenantID, by, domain.PermConfig, func(t *domain.Tenant) error {
		v, err := t.Version(versionID)
		if err != nil {
			return err
		}
		if len(domain.ConfigChanges(t.Config, v.Config)) == 0 {
			return errors.New("esa versión es igual a la configuración vigente")
		}
		return applyConfig(t, by, v.Config, v.ID)
	})
}

// applyConfig valida la configuración nueva, migra los datos existentes y registra la versión.
func applyConfig(t *domain.Tenant, by int, c domain.Config, restoredFrom int) error {
	c = c.Clone() // la cuenta se queda con su propia copia
	// Los estados de pausa se validan después de renombrarlos: pueden venir con el nombre viejo de un estado renombrado.
	pause := c.PauseStates
	c.PauseStates = nil
	if err := c.Validate(); err != nil {
		return err
	}
	// Ítems renombrados en el lugar: los datos existentes siguen al nombre nuevo (ver renames).
	states, services := renames(t.Config.States, c.States), renames(t.Config.Services, c.Services)
	requestTypes := renames(t.Config.RequestTypeNames(), c.RequestTypeNames())
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

	// Guardar sin cambios no crea una versión, y durante el onboarding todavía no hay historial (ver Setup.Finish).
	changes := domain.ConfigChanges(t.Config, c)
	versioned := t.Onboarded && len(changes) > 0
	if versioned {
		t.EnsureFirstVersion(time.Now(), by) // antes de pisarla: la primera versión es la configuración de hasta ahora
	}
	t.Config = c
	if versioned {
		t.AddVersion(time.Now(), by, changes, restoredFrom)
	}
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
		// Una solicitud sigue al ítem renombrado. Si el ítem se quitó del catálogo, la solicitud conserva el nombre:
		// el catálogo dice qué se puede pedir de acá en más, no reescribe lo que ya se pidió.
		if n, ok := requestTypes[inc.RequestType]; ok && inc.IsRequest() {
			inc.RequestType = n
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
