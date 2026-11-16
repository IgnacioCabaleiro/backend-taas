package service

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"taas-backend/internal/domain"
)

type Incidents struct{ base }

// Create registra un incidente: queda en el estado inicial, con el evento de registro y, si el módulo SLA
// está activo, los vencimientos de la primera respuesta y de la resolución. Si continúa uno cerrado
// (relatedIncidentId), queda relacionado con él.
func (s *Incidents) Create(tenantID, by int, in domain.NewIncident) (inc *domain.Incident, err error) {
	err = s.update(tenantID, by, domain.PermCreate, func(t *domain.Tenant) error {
		// Asignar y vincular son decisiones de quien resuelve; quien solo reporta no las toma.
		if !t.Can(by, domain.PermResolve) {
			in.AssigneeID, in.ProblemID = 0, 0
		}
		var reqType *domain.RequestType
		switch in.Kind {
		case "", domain.KindIncident:
			in.Kind, in.RequestType = domain.KindIncident, ""
			if in.Title = strings.TrimSpace(in.Title); in.Title == "" {
				return errors.New("contá qué está pasando")
			}
			if !domain.Has(t.Config.Services, in.Service) {
				return errors.New("elegí un servicio del catálogo")
			}
			switch {
			case in.Impact != "" || in.Urgency != "":
				p, ok := domain.PriorityFor(in.Impact, in.Urgency)
				if !ok {
					return errors.New("elegí el impacto y la urgencia")
				}
				in.Priority = p
			case !domain.Has(domain.Priorities, in.Priority):
				return errors.New("prioridad inválida")
			}
		case domain.KindRequest:
			// Una solicitud es un pedido estándar: sale del catálogo, que fija su plazo. No tiene servicio afectado,
			// prioridad ni problema (no es una falla).
			if reqType = t.Config.RequestType(in.RequestType); reqType == nil {
				return errors.New("elegí una solicitud del catálogo")
			}
			if in.Title = strings.TrimSpace(in.Title); in.Title == "" {
				in.Title = reqType.Name // el título es opcional: si no hay, alcanza con qué se pide
			}
			in.Service, in.Priority, in.Impact, in.Urgency, in.ProblemID = "", "", "", "", 0
		default:
			return errors.New("tipo de ticket inválido: incidente o solicitud")
		}
		if err := assignable(t, in.AssigneeID); err != nil {
			return err
		}
		fields, err := customFields(t.Config.Fields, in.Fields)
		if err != nil {
			return err
		}
		// Se valida el problema antes de agregar el incidente: si falla, la cuenta no tiene que quedar tocada.
		if err := linkable(t, in.ProblemID); err != nil {
			return err
		}
		related, err := relatable(t, in.RelatedIncidentID, in.Kind, by)
		if err != nil {
			return err
		}
		now := time.Now()
		inc = &domain.Incident{ID: len(t.Incidents) + 1, Kind: in.Kind, RequestType: in.RequestType, Title: in.Title,
			Description: in.Description, Service: in.Service, Priority: in.Priority, Impact: in.Impact, Urgency: in.Urgency,
			Status: t.Config.States[0], Fields: fields, CreatedBy: by, CreatedAt: now,
			History: []domain.Event{}, Attachments: []domain.Attachment{}, RelatedIncidentID: in.RelatedIncidentID}
		what := "Incidente registrado"
		if reqType != nil {
			what = "Solicitud registrada: " + reqType.Name
		}
		if related != nil {
			inc.Log(now, by, domain.EvCreated, "%s (relacionado con #%d)", what, related.ID)
			// La única escritura sobre un cerrado: queda constancia de su continuación.
			related.Log(now, by, domain.EvRelated, "Se registró #%d relacionado", inc.ID)
		} else {
			inc.Log(now, by, domain.EvCreated, "%s", what)
		}
		switch {
		case !t.Config.Modules.SLA:
		case reqType != nil:
			// El plazo de una solicitud es el de su ítem del catálogo. No tiene vencimiento de primera respuesta:
			// lo que se mide es que se cumpla a tiempo.
			due := now.Add(time.Duration(reqType.Hours) * time.Hour)
			inc.DueAt = &due
		default:
			due := now.Add(time.Duration(t.Config.SLAHours[in.Priority]) * time.Hour)
			resp := now.Add(time.Duration(t.Config.ResponseHours[in.Priority]) * time.Hour)
			inc.DueAt, inc.ResponseDueAt = &due, &resp
		}
		assign(t, inc, in.AssigneeID, by, now)
		t.Incidents = append(t.Incidents, inc)
		link(t, inc, in.ProblemID, by, now)
		return nil
	})
	return inc, err
}

// relatable verifica que se pueda relacionar el ticket nuevo (de tipo kind) con id (0 = ninguno): tiene que existir,
// ser del mismo tipo y estar cerrado, y quien solo reporta solo puede relacionarlo con uno suyo.
func relatable(t *domain.Tenant, id int, kind string, by int) (*domain.Incident, error) {
	if id == 0 {
		return nil, nil
	}
	r, err := t.Incident(id)
	if err != nil {
		return nil, fmt.Errorf("el incidente relacionado #%d no existe", id)
	}
	if r.Kind != kind {
		return nil, errors.New("solo se puede relacionar con un ticket del mismo tipo: un incidente con un incidente, una solicitud con una solicitud")
	}
	if !t.Can(by, domain.PermResolve) && r.CreatedBy != by {
		return nil, domain.Denied("solo podés relacionarlo con un incidente tuyo")
	}
	if !r.Closed() {
		return nil, errors.New("solo se puede relacionar con un incidente cerrado")
	}
	return r, nil
}

// customFields valida los campos propios de la cuenta y descarta los que no existen.
func customFields(defs []domain.Field, in map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for _, f := range defs {
		v := strings.TrimSpace(in[f.Key])
		switch {
		case v == "" && f.Required:
			return nil, fmt.Errorf("falta completar: %s", f.Label)
		case v == "":
			continue
		case f.Type == "number":
			if _, err := strconv.ParseFloat(v, 64); err != nil {
				return nil, fmt.Errorf("%s tiene que ser un número", f.Label)
			}
		case f.Type == "select" && !domain.Has(f.Options, v):
			return nil, fmt.Errorf("opción inválida en %s", f.Label)
		}
		out[f.Key] = v
	}
	return out, nil
}

// Update cambia el estado, el asignado o el problema de un incidente: el diagnóstico y, al pasar al estado
// de resolución, la resolución (con código y nota). Valida todo el pedido antes de aplicar cualquier cambio.
func (s *Incidents) Update(tenantID, by, id int, in domain.IncidentPatch) error {
	return s.update(tenantID, by, domain.PermResolve, func(t *domain.Tenant) error {
		inc, err := t.Incident(id)
		if err != nil {
			return err
		}
		if inc.Closed() {
			return domain.ErrClosed
		}
		if in.AssigneeID != nil {
			if err := assignable(t, *in.AssigneeID); err != nil {
				return err
			}
		}
		if in.ProblemID != nil && *in.ProblemID != 0 && inc.IsRequest() {
			return errors.New("una solicitud no se vincula a un problema: no es una falla")
		}
		if in.ProblemID != nil && *in.ProblemID != inc.ProblemID {
			if err := linkable(t, *in.ProblemID); err != nil {
				return err
			}
		}
		if in.Status != nil && !domain.Has(t.Config.States, *in.Status) {
			return errors.New("estado inválido")
		}
		// Reenviar el estado que ya tiene no es un cambio.
		moving := in.Status != nil && *in.Status != inc.Status
		if moving && inc.Resolved() {
			return errors.New("el incidente está resuelto: reabrilo para seguir trabajando")
		}
		resolving := moving && *in.Status == t.ResolvedState()
		if resolving {
			if err := resolution(inc.Kind, in.ResolutionCode, in.ResolutionNote); err != nil {
				return err
			}
		}

		now := time.Now()
		if in.AssigneeID != nil {
			assign(t, inc, *in.AssigneeID, by, now)
		}
		if in.ProblemID != nil {
			link(t, inc, *in.ProblemID, by, now)
		}
		switch from := inc.Status; {
		case resolving:
			setStatus(t, inc, *in.Status, now)
			resolve(t, inc, from, in.ResolutionCode, strings.TrimSpace(in.ResolutionNote), by, now)
		case moving:
			setStatus(t, inc, *in.Status, now)
			inc.Log(now, by, domain.EvStatus, "De %s a %s", from, inc.Status)
		}
		return nil
	})
}

// resolution valida el código y la nota con los que se resuelve un ticket de tipo kind: cada tipo tiene los suyos.
func resolution(kind, code, note string) error {
	if !domain.Has(domain.ResolutionCodesFor(kind), code) {
		return errors.New("elegí un código de resolución válido")
	}
	if strings.TrimSpace(note) == "" {
		if kind == domain.KindRequest {
			return errors.New("contá cómo se resolvió la solicitud")
		}
		return errors.New("contá cómo se resolvió el incidente")
	}
	return nil
}

// Comment agrega una respuesta (pública) o una nota interna al historial del incidente.
// Quien lo atiende puede publicar las dos; quien lo reportó, solo respuestas y solo en sus incidentes.
// La primera respuesta pública de quien lo atiende es la primera respuesta del SLA. Si quien lo reportó
// responde estando resuelto, el incidente ya no se cierra solo (AutoCloseHold).
func (s *Incidents) Comment(tenantID, by, id int, text string, public bool) error {
	return s.modify(tenantID, func(t *domain.Tenant) error {
		inc, err := t.Incident(id)
		if err != nil {
			return err
		}
		resolver := t.Can(by, domain.PermResolve)
		switch {
		case resolver:
		case t.Can(by, domain.PermCreate) && inc.CreatedBy == by:
			if !public {
				return domain.Denied("las notas internas son para quien atiende el incidente: publicá una respuesta")
			}
		default:
			return domain.ErrForbidden
		}
		if inc.Closed() {
			return domain.ErrClosed
		}
		if text = strings.TrimSpace(text); text == "" {
			return errors.New("el comentario está vacío")
		}
		now := time.Now()
		switch {
		case !public:
			inc.Log(now, by, domain.EvNote, "%s", text)
			return nil
		case resolver:
			inc.Respond(now)
		case inc.Resolved():
			inc.AutoCloseHold = true
		}
		inc.Log(now, by, domain.EvReply, "%s", text)
		return nil
	})
}

// Reopen reabre un incidente resuelto (todavía sin cerrar) porque el servicio no quedó restablecido:
// vuelve al estado inicial y la reapertura cuenta contra el SLA (el vencimiento original no cambia).
// Si su asignado ya no tiene resolver o ya no existe, queda sin asignar.
// Un cerrado no se reabre: se registra un incidente relacionado.
func (s *Incidents) Reopen(tenantID, by, id int, reason string) error {
	return s.update(tenantID, by, domain.PermResolve, func(t *domain.Tenant) error {
		inc, err := t.Incident(id)
		if err != nil {
			return err
		}
		if inc.Closed() {
			return domain.ErrClosed
		}
		if reason = strings.TrimSpace(reason); reason == "" {
			return errors.New("contá por qué se reabre el incidente")
		}
		if !inc.Resolved() {
			return errors.New("el incidente no está resuelto")
		}
		now, from := time.Now(), inc.Status
		if inc.ResolutionCode == "workaround" {
			workaroundFailed(t, inc, by)
		}
		setStatus(t, inc, t.Config.States[0], now)
		inc.ResolvedAt, inc.ResolutionCode, inc.ResolutionNote = nil, "", ""
		inc.ReopenCount++
		inc.AutoCloseHold = false
		inc.Log(now, by, domain.EvReopened, "De %s a %s: %s", from, inc.Status, reason)
		// Si quien lo tenía asignado ya no atiende incidentes (perdió resolver mientras estaba resuelto) o ya no
		// existe (DeleteUser solo desasigna los abiertos), vuelve a la cola: solo se asigna a quien puede atenderlo
		// (ver assignable).
		switch u := t.User(inc.AssigneeID); {
		case inc.AssigneeID == 0:
		case u == nil:
			inc.AssigneeID = 0
			inc.Log(now, by, domain.EvAssigned, "Sin asignar (el usuario asignado ya no existe)")
		case !t.Can(u.ID, domain.PermResolve):
			inc.AssigneeID = 0
			inc.Log(now, by, domain.EvAssigned, "Sin asignar (%s ya no atiende incidentes)", u.Name)
		}
		return nil
	})
}

// Close cierra un incidente resuelto: quien gestiona confirma que el servicio quedó restablecido.
func (s *Incidents) Close(tenantID, by, id int) error {
	return s.update(tenantID, by, domain.PermResolve, func(t *domain.Tenant) error {
		inc, err := t.Incident(id)
		if err != nil {
			return err
		}
		if inc.Closed() {
			return domain.ErrClosed
		}
		if !inc.Resolved() {
			return errors.New("el incidente no está resuelto")
		}
		closeIncident(t, inc, by, time.Now())
		return nil
	})
}

// assignable verifica que se pueda asignar el incidente al usuario: tiene que existir y poder atender incidentes
// (resolver). 0 significa "sin asignar" y siempre se puede.
func assignable(t *domain.Tenant, userID int) error {
	if userID == 0 {
		return nil
	}
	if t.User(userID) == nil {
		return fmt.Errorf("usuario %d: %w", userID, domain.ErrNotFound)
	}
	if !t.Can(userID, domain.PermResolve) {
		return errors.New("solo se puede asignar a quien atiende incidentes")
	}
	return nil
}

// assign cambia el asignado (0 = sin asignar) y lo deja en el historial. Requiere assignable.
func assign(t *domain.Tenant, inc *domain.Incident, userID, by int, now time.Time) {
	if inc.AssigneeID == userID {
		return
	}
	inc.AssigneeID = userID
	if u := t.User(userID); u != nil {
		inc.Log(now, by, domain.EvAssigned, "Asignado a %s", u.Name)
	} else {
		inc.Log(now, by, domain.EvAssigned, "Sin asignar")
	}
}

// linkable verifica que se pueda vincular un incidente al problema. 0 significa "sin problema" y siempre se puede.
func linkable(t *domain.Tenant, problemID int) error {
	if problemID == 0 {
		return nil
	}
	if !t.Config.Modules.Problems {
		return errors.New("la gestión de problemas no está activada en esta cuenta")
	}
	p, err := t.Problem(problemID)
	if err != nil {
		return err
	}
	if p.Status == domain.Resuelto {
		return errors.New("el problema ya está resuelto")
	}
	return nil
}

// link vincula (o con 0, desvincula) el incidente y lo deja en el historial de los dos. Requiere linkable.
func link(t *domain.Tenant, inc *domain.Incident, problemID, by int, now time.Time) {
	if inc.ProblemID == problemID {
		return
	}
	if old, err := t.Problem(inc.ProblemID); err == nil {
		old.Log(by, "", "#%d desvinculado del problema", inc.ID)
		inc.Log(now, by, domain.EvProblem, "Desvinculado del problema P-%d", old.ID)
	}
	inc.ProblemID = problemID
	if p, err := t.Problem(problemID); err == nil {
		p.Log(by, "", "#%d vinculado al problema", inc.ID)
		inc.Log(now, by, domain.EvProblem, "Vinculado al problema P-%d", p.ID)
	}
}

// setStatus cambia el estado del incidente y detiene o reanuda el reloj del SLA. El evento lo deja quien
// llama: un cambio de estado, una resolución o una reapertura. Requiere que el estado exista en la configuración.
func setStatus(t *domain.Tenant, inc *domain.Incident, status string, now time.Time) {
	inc.Status = status
	inc.SyncPause(t.Config.PauseStates, now)
}

// resolve registra la resolución de un incidente que ya está en el estado de resolución (ver setStatus),
// con un solo evento. Con duplicado o cancelado, además lo cierra: no hay nada que confirmar.
// Requiere el código y la nota validados (ver resolution).
func resolve(t *domain.Tenant, inc *domain.Incident, from, code, note string, by int, now time.Time) {
	inc.ResolvedAt, inc.ResolutionCode, inc.ResolutionNote = &now, code, note
	inc.Log(now, by, domain.EvResolved, "De %s a %s · %s: %s", from, inc.Status, domain.ResolutionLabels[code], note)
	if p, err := t.Problem(inc.ProblemID); err == nil && code == "workaround" && p.Workaround != "" && p.Status != domain.Resuelto {
		p.WorkaroundUses = append(p.WorkaroundUses, domain.WorkaroundUse{IncidentID: inc.ID, At: now, UserID: by})
		p.Log(by, "", "#%d resuelto con workaround", inc.ID)
	}
	if domain.Has(domain.ClosingCodes, code) {
		closeIncident(t, inc, by, now)
	}
}

// Rate guarda la valoración de quien reportó el incidente, una vez resuelto (también si ya se cerró).
// Se valora una sola vez: es la foto de cómo terminó la atención.
func (s *Incidents) Rate(tenantID, by, id, rating int, comment string) error {
	return s.modify(tenantID, func(t *domain.Tenant) error {
		inc, err := t.Incident(id)
		if err != nil {
			return err
		}
		if inc.CreatedBy != by || !t.Can(by, domain.PermCreate) {
			return domain.Denied("valora la atención quien reportó el incidente")
		}
		if inc.ResolvedAt == nil {
			return errors.New("el incidente todavía no está resuelto")
		}
		if inc.Rating != 0 {
			return errors.New("ya valoraste este incidente")
		}
		if rating < 1 || rating > 5 {
			return errors.New("elegí una valoración de 1 a 5")
		}
		now := time.Now()
		inc.Rating, inc.RatingComment, inc.RatedAt = rating, strings.TrimSpace(comment), &now
		return nil
	})
}

// workaroundFailed marca como fallido el último uso del workaround en inc: se reabrió, así que no alcanzó.
func workaroundFailed(t *domain.Tenant, inc *domain.Incident, by int) {
	p, err := t.Problem(inc.ProblemID)
	if err != nil {
		return
	}
	for i := len(p.WorkaroundUses) - 1; i >= 0; i-- {
		if u := &p.WorkaroundUses[i]; u.IncidentID == inc.ID && !u.Failed {
			u.Failed = true
			p.Log(by, "", "El workaround no alcanzó en #%d: se reabrió", inc.ID)
			return
		}
	}
}

// ApplyWorkaround resuelve un incidente abierto con el workaround de un error conocido, en un solo paso:
// lo vincula al problema si hace falta, le responde a quien lo reportó con el workaround (cuenta como primera
// respuesta) y lo resuelve con el código "workaround". Si después se reabre, el uso queda como fallido.
func (s *Incidents) ApplyWorkaround(tenantID, by, id, problemID int) error {
	return s.update(tenantID, by, domain.PermResolve, func(t *domain.Tenant) error {
		inc, err := t.Incident(id)
		if err != nil {
			return err
		}
		if inc.Closed() {
			return domain.ErrClosed
		}
		if inc.ResolvedAt != nil {
			return errors.New("el incidente ya está resuelto")
		}
		if err := linkable(t, problemID); err != nil {
			return err
		}
		p, err := t.Problem(problemID)
		if err != nil {
			return err
		}
		if p.Status != domain.ErrorConocido || strings.TrimSpace(p.Workaround) == "" {
			return errors.New("ese problema no tiene un workaround publicado")
		}
		now, from := time.Now(), inc.Status
		link(t, inc, p.ID, by, now)
		inc.Respond(now)
		inc.Log(now, by, domain.EvReply, "Mientras resolvemos la causa de fondo, esto te permite seguir trabajando: %s", p.Workaround)
		setStatus(t, inc, t.ResolvedState(), now)
		resolve(t, inc, from, "workaround", p.Workaround, by, now)
		return nil
	})
}

// closeIncident cierra un incidente resuelto a mano (el automático es domain.Tenant.AutoClose).
func closeIncident(t *domain.Tenant, inc *domain.Incident, by int, now time.Time) {
	inc.ClosedAt = &now
	inc.Log(now, by, domain.EvClosed, "Cerrado por %s", t.User(by).Name)
}
