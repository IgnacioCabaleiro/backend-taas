package handler

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"

	"taas-backend/internal/domain"
)

// Adjuntos de los incidentes. Subir y quitar responden con el estado de la cuenta, como el resto;
// descargar responde con el archivo.

// uploadLimit: el tope del body de una subida (el archivo más el armado del multipart).
const uploadLimit = domain.MaxAttachmentSize + 64<<10

func (h *Handler) addAttachment(r *http.Request, tenantID, userID int) error {
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		return errors.New("mandá el archivo en el campo «file»")
	}
	f, head, err := r.FormFile("file")
	if err != nil {
		return errors.New("mandá el archivo en el campo «file»")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, domain.MaxAttachmentSize+1))
	if err != nil {
		return errors.New("el archivo llegó incompleto")
	}
	return h.svc.Attachments.Add(tenantID, userID, pathID(r), head.Filename, data)
}

func (h *Handler) removeAttachment(r *http.Request, tenantID, userID int) error {
	return h.svc.Attachments.Remove(tenantID, userID, pathID(r), r.PathValue("aid"))
}

// downloadAttachment sirve el archivo siempre como descarga y con el tipo que fijó el servidor, sin que el
// navegador lo interprete: un HTML subido como adjunto no se ejecuta.
func (h *Handler) downloadAttachment(w http.ResponseWriter, r *http.Request) {
	tenantID, userID, err := h.svc.Auth.Authenticate(bearer(r))
	if err != nil {
		fail(w, err)
		return
	}
	a, f, err := h.svc.Attachments.Open(tenantID, userID, pathID(r), r.PathValue("aid"))
	if err != nil {
		fail(w, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", a.ContentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": a.Name}))
	w.Header().Set("Content-Length", strconv.FormatInt(a.Size, 10))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	io.Copy(w, f)
}
