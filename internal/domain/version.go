package domain

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ConfigVersion: una foto de la configuración de la cuenta. El historial solo crece: volver a una versión
// anterior crea una versión nueva con ese contenido, así siempre queda quién cambió qué y cuándo
// (y se puede deshacer también la vuelta atrás).
type ConfigVersion struct {
	ID           int       `json:"id"` // correlativo dentro de la cuenta
	At           time.Time `json:"at"`
	UserID       int       `json:"userId"`
	Changes      []string  `json:"changes"`      // qué cambió respecto de la versión anterior
	RestoredFrom int       `json:"restoredFrom"` // versión de la que es copia; 0 = edición normal
	Config       Config    `json:"config"`
}

// Clone devuelve una copia independiente: las versiones no comparten listas ni mapas con la configuración vigente.
func (c Config) Clone() Config {
	data, err := json.Marshal(c)
	out := Config{}
	if err == nil {
		err = json.Unmarshal(data, &out)
	}
	if err != nil {
		panic(fmt.Sprintf("clonar la configuración: %v", err))
	}
	return out
}

// Version busca una versión por su ID.
func (t *Tenant) Version(id int) (*ConfigVersion, error) {
	for i := range t.ConfigVersions {
		if t.ConfigVersions[i].ID == id {
			return &t.ConfigVersions[i], nil
		}
	}
	return nil, fmt.Errorf("versión %d: %w", id, ErrNotFound)
}

// AddVersion registra la configuración vigente como versión nueva.
func (t *Tenant) AddVersion(at time.Time, by int, changes []string, restoredFrom int) {
	t.ConfigVersions = append(t.ConfigVersions, ConfigVersion{ID: len(t.ConfigVersions) + 1, At: at, UserID: by,
		Changes: changes, RestoredFrom: restoredFrom, Config: t.Config.Clone()})
}

// EnsureFirstVersion crea la primera versión si la cuenta todavía no tiene historial.
func (t *Tenant) EnsureFirstVersion(at time.Time, by int) {
	if len(t.ConfigVersions) == 0 {
		t.AddVersion(at, by, []string{"Configuración inicial"}, 0)
	}
}

func sameJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// listChanges describe las diferencias entre dos listas. El orden importa: en los estados es el flujo.
func listChanges(what string, old, new []string) []string {
	var added, removed []string
	for _, s := range new {
		if !Has(old, s) {
			added = append(added, s)
		}
	}
	for _, s := range old {
		if !Has(new, s) {
			removed = append(removed, s)
		}
	}
	out := []string{}
	if len(added) > 0 && len(added) == len(removed) { // misma cantidad: se lee mejor como renombre
		for i := range added {
			out = append(out, fmt.Sprintf("%s: %s → %s", what, removed[i], added[i]))
		}
		return out
	}
	if len(added) > 0 {
		out = append(out, fmt.Sprintf("%s agregado: %s", what, strings.Join(added, ", ")))
	}
	if len(removed) > 0 {
		out = append(out, fmt.Sprintf("%s eliminado: %s", what, strings.Join(removed, ", ")))
	}
	if len(out) == 0 && !sameJSON(old, new) {
		out = append(out, fmt.Sprintf("%s: cambió el orden", what))
	}
	return out
}

func onOff(on bool) string {
	if on {
		return "activado"
	}
	return "desactivado"
}

// ConfigChanges resume qué cambió entre dos configuraciones, en palabras de quien la administra.
// Vacío quiere decir que son iguales.
func ConfigChanges(old, new Config) []string {
	out := []string{}
	if old.Modules.SLA != new.Modules.SLA {
		out = append(out, "SLA "+onOff(new.Modules.SLA))
	}
	if old.Modules.Problems != new.Modules.Problems {
		out = append(out, "Gestión de problemas "+onOff(new.Modules.Problems))
	}
	out = append(out, listChanges("Servicio", old.Services, new.Services)...)
	out = append(out, listChanges("Estado", old.States, new.States)...)
	for _, p := range Priorities {
		if old.SLAHours[p] != new.SLAHours[p] {
			out = append(out, fmt.Sprintf("SLA de prioridad %s: %d h → %d h", p, old.SLAHours[p], new.SLAHours[p]))
		}
		if old.ResponseHours[p] != new.ResponseHours[p] {
			out = append(out, fmt.Sprintf("Primera respuesta de prioridad %s: %d h → %d h", p, old.ResponseHours[p], new.ResponseHours[p]))
		}
	}
	labels := func(fs []Field) []string {
		l := []string{}
		for _, f := range fs {
			l = append(l, f.Label)
		}
		return l
	}
	fields := listChanges("Campo", labels(old.Fields), labels(new.Fields))
	if len(fields) == 0 && !sameJSON(old.Fields, new.Fields) {
		fields = []string{"Campos del ticket modificados"}
	}
	out = append(out, fields...)
	if !sameJSON(old.PauseStates, new.PauseStates) {
		out = append(out, "Estados que pausan el SLA: "+orNone(new.PauseStates))
	}
	if old.RecurrenceMin != new.RecurrenceMin || old.RecurrenceDays != new.RecurrenceDays {
		out = append(out, fmt.Sprintf("Recurrencia: %d incidentes en %d días", new.RecurrenceMin, new.RecurrenceDays))
	}
	if old.AutoCloseDays != new.AutoCloseDays {
		out = append(out, fmt.Sprintf("Cierre automático: %d → %d días", old.AutoCloseDays, new.AutoCloseDays))
	}
	return out
}

func orNone(list []string) string {
	if len(list) == 0 {
		return "ninguno"
	}
	return strings.Join(list, ", ")
}
