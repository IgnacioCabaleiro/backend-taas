package domain

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// Un adjunto es un archivo que acompaña a un incidente (captura, log, PDF). Acá vive solo su descripción;
// el contenido se guarda aparte (ver repository.Blobs) bajo la clave que arma Incident.BlobKey.
type Attachment struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	ContentType string    `json:"contentType"`
	UploadedBy  int       `json:"uploadedBy"`
	UploadedAt  time.Time `json:"uploadedAt"`
}

const (
	MaxAttachmentSize = 5 << 20 // bytes por archivo
	MaxAttachments    = 10      // por incidente
)

// attachmentTypes: extensiones permitidas y el tipo con el que se sirven. El tipo lo fija el servidor
// por la extensión: nunca se confía en el que declara el cliente.
var attachmentTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".pdf":  "application/pdf",
	".txt":  "text/plain; charset=utf-8",
	".log":  "text/plain; charset=utf-8",
	".csv":  "text/csv; charset=utf-8",
	".zip":  "application/zip",
}

// AttachmentExts: las extensiones que se aceptan, para los mensajes de error y el selector del front.
const AttachmentExts = ".png, .jpg, .jpeg, .gif, .webp, .pdf, .txt, .log, .csv, .zip"

// ErrAttachment: el archivo no se puede adjuntar (se responde 400 con el motivo).
var ErrAttachment = errors.New("adjunto inválido")

// attachmentError es un ErrAttachment cuyo mensaje es solo el motivo, que es lo que se le muestra a quien subió el archivo.
type attachmentError string

func (e attachmentError) Error() string        { return string(e) }
func (e attachmentError) Is(target error) bool { return target == ErrAttachment }

// BadAttachment: el archivo no se puede adjuntar, por este motivo.
func BadAttachment(format string, a ...any) error { return attachmentError(fmt.Sprintf(format, a...)) }

func badAttachment(format string, a ...any) error { return BadAttachment(format, a...) }

// CleanFileName deja solo el nombre del archivo: sin carpetas ni caracteres de control, de hasta 100 caracteres
// (conserva la extensión).
func CleanFileName(name string) string {
	name = name[strings.LastIndexAny(name, `/\`)+1:]
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 100 {
		ext := []rune(filepath.Ext(name))
		if len(ext) > 10 {
			ext = nil
		}
		name = string(r[:100-len(ext)]) + string(ext)
	}
	return name
}

// ValidateAttachment verifica el nombre, el tamaño y que el contenido sea de lo que dice la extensión.
// Devuelve el nombre limpio y el tipo con el que se va a servir.
func ValidateAttachment(name string, data []byte) (cleaned, contentType string, err error) {
	cleaned = CleanFileName(name)
	if cleaned == "" || cleaned == "." || cleaned == ".." {
		return "", "", badAttachment("el archivo no tiene nombre")
	}
	ext := strings.ToLower(filepath.Ext(cleaned))
	contentType, ok := attachmentTypes[ext]
	if !ok {
		return "", "", badAttachment("no se aceptan archivos %q: usá %s", ext, AttachmentExts)
	}
	switch {
	case len(data) == 0:
		return "", "", badAttachment("el archivo está vacío")
	case len(data) > MaxAttachmentSize:
		return "", "", badAttachment("el archivo pesa más de %d MB", MaxAttachmentSize>>20)
	}
	if !matchesContent(ext, data) {
		return "", "", badAttachment("el contenido de %q no coincide con su extensión", cleaned)
	}
	return cleaned, contentType, nil
}

// matchesContent: los binarios tienen que empezar como lo que dice la extensión; los de texto, no tener bytes nulos.
func matchesContent(ext string, data []byte) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		want := attachmentTypes[ext]
		if ext == ".jpeg" {
			want = "image/jpeg"
		}
		return http.DetectContentType(data) == want
	case ".pdf":
		return bytes.HasPrefix(data, []byte("%PDF-"))
	case ".zip":
		return bytes.HasPrefix(data, []byte("PK"))
	default:
		return !bytes.Contains(data, []byte{0})
	}
}

// BlobKey: dónde se guarda el contenido de un adjunto. Solo usa números y el ID que genera el servidor.
func (inc *Incident) BlobKey(tenantID int, attachmentID string) string {
	return fmt.Sprintf("%d/%d/%s", tenantID, inc.ID, attachmentID)
}

// Attachment busca un adjunto del incidente.
func (inc *Incident) Attachment(id string) (*Attachment, int, error) {
	for k := range inc.Attachments {
		if inc.Attachments[k].ID == id {
			return &inc.Attachments[k], k, nil
		}
	}
	return nil, -1, fmt.Errorf("adjunto %s: %w", id, ErrNotFound)
}
