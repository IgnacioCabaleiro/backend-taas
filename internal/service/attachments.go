package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"time"

	"taas-backend/internal/domain"
	"taas-backend/internal/repository"
)

// Attachments: archivos adjuntos de los incidentes. Los metadatos viven en el incidente y el contenido en Blobs.
type Attachments struct {
	base
	blobs repository.Blobs
}

// canAttach: quien atiende el incidente adjunta en cualquiera; quien lo reportó, solo en los suyos.
func canAttach(t *domain.Tenant, by int, inc *domain.Incident) error {
	switch {
	case t.Can(by, domain.PermResolve):
	case t.Can(by, domain.PermCreate) && inc.CreatedBy == by:
	default:
		return domain.ErrForbidden
	}
	if inc.Closed() {
		return domain.ErrClosed
	}
	return nil
}

// Add adjunta un archivo al incidente y lo deja en el historial. Valida todo antes de guardar el contenido.
// Si quien lo reportó adjunta estando resuelto, el incidente ya no se cierra solo (igual que si respondiera).
func (s *Attachments) Add(tenantID, by, incidentID int, name string, data []byte) error {
	var stored string
	err := s.modify(tenantID, func(t *domain.Tenant) error {
		inc, err := t.Incident(incidentID)
		if err != nil {
			return err
		}
		if err := canAttach(t, by, inc); err != nil {
			return err
		}
		if len(inc.Attachments) >= domain.MaxAttachments {
			return domain.BadAttachment("el incidente ya tiene %d adjuntos: quitá alguno antes de agregar otro", domain.MaxAttachments)
		}
		name, contentType, err := domain.ValidateAttachment(name, data)
		if err != nil {
			return err
		}
		id, err := newAttachmentID()
		if err != nil {
			return err
		}
		key := inc.BlobKey(tenantID, id)
		if err := s.blobs.Put(key, data); err != nil {
			return fmt.Errorf("guardar el adjunto %s: %v: %w", key, err, domain.ErrInternal)
		}
		stored = key
		now := time.Now()
		inc.Attachments = append(inc.Attachments, domain.Attachment{ID: id, Name: name, Size: int64(len(data)),
			ContentType: contentType, UploadedBy: by, UploadedAt: now})
		inc.Log(now, by, domain.EvAttached, "Adjuntó %s", name)
		if !t.Can(by, domain.PermResolve) && inc.Resolved() {
			inc.AutoCloseHold = true
		}
		return nil
	})
	if err != nil && stored != "" { // no se pudo guardar la cuenta: no queda un archivo huérfano
		s.drop(stored)
	}
	return err
}

// Remove quita un adjunto. Quien atiende puede quitar cualquiera; quien lo reportó, solo los que subió él.
func (s *Attachments) Remove(tenantID, by, incidentID int, attachmentID string) error {
	var key string
	err := s.modify(tenantID, func(t *domain.Tenant) error {
		inc, err := t.Incident(incidentID)
		if err != nil {
			return err
		}
		if err := canAttach(t, by, inc); err != nil {
			return err
		}
		a, k, err := inc.Attachment(attachmentID)
		if err != nil {
			return err
		}
		if a.UploadedBy != by && !t.Can(by, domain.PermResolve) {
			return domain.Denied("solo podés quitar los adjuntos que subiste vos")
		}
		now := time.Now()
		inc.Log(now, by, domain.EvAttached, "Quitó el adjunto %s", a.Name)
		key = inc.BlobKey(tenantID, a.ID)
		inc.Attachments = append(inc.Attachments[:k:k], inc.Attachments[k+1:]...)
		return nil
	})
	if err == nil {
		s.drop(key)
	}
	return err
}

// Open devuelve el adjunto y su contenido. Lo puede abrir cualquiera que vea el incidente.
// El que llama cierra el contenido.
func (s *Attachments) Open(tenantID, by, incidentID int, attachmentID string) (domain.Attachment, io.ReadCloser, error) {
	s.mu.Lock()
	t, err := s.tenants.Get(tenantID)
	var a domain.Attachment
	var key string
	if err == nil {
		var inc *domain.Incident
		if inc, err = t.Incident(incidentID); err == nil {
			switch {
			case !t.SeesAll(by) && inc.CreatedBy != by:
				err = domain.ErrForbidden
			default:
				var found *domain.Attachment
				if found, _, err = inc.Attachment(attachmentID); err == nil {
					a, key = *found, inc.BlobKey(tenantID, found.ID)
				}
			}
		}
	}
	s.mu.Unlock()
	if err != nil {
		return a, nil, err
	}
	f, err := s.blobs.Open(key)
	if errors.Is(err, domain.ErrNotFound) {
		return a, nil, err
	}
	if err != nil {
		return a, nil, fmt.Errorf("abrir el adjunto %s: %v: %w", key, err, domain.ErrInternal)
	}
	return a, f, nil
}

// drop borra el contenido; si falla solo queda un archivo suelto, así que se registra y no se corta el pedido.
func (s *Attachments) drop(key string) {
	if err := s.blobs.Delete(key); err != nil {
		log.Printf("borrar el adjunto %s: %v", key, err)
	}
}

func newAttachmentID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generar el ID del adjunto: %v: %w", err, domain.ErrInternal)
	}
	return hex.EncodeToString(b), nil
}
