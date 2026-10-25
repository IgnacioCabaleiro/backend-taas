package handler

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"taas-backend/internal/domain"
	"taas-backend/internal/service"
)

var (
	pngData = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")
	pdfData = []byte("%PDF-1.4\n%fake\n")
)

// upload manda un archivo como multipart al incidente.
func upload(t *testing.T, h http.Handler, token string, incident int, name string, data []byte) (int, service.View) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", name)
	fw.Write(data)
	mw.Close()
	r := httptest.NewRequest("POST", "/api/incidents/"+itoa(incident)+"/attachments", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("Authorization", "Bearer "+token)
	return decodeView(t, h, r)
}

func decodeView(t *testing.T, h http.Handler, r *http.Request) (int, service.View) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	var v service.View
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
	}
	return rec.Code, v
}

func do(h http.Handler, method, path, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func itoa(n int) string { return strconv.Itoa(n) }

// El flujo completo: el solicitante sube una captura, el agente la descarga, y quedan en el historial.
func TestAttachmentUploadAndDownload(t *testing.T) {
	h, svc, tok := newServer(t)
	in := domain.NewIncident{Title: "x", Service: "VPN", Priority: "alta", Fields: map[string]string{"sede": "Remoto"}}
	svc.Incidents.Create(1, 3, in) // #1 del solicitante

	code, v := upload(t, h, tok[3], 1, "error.png", pngData)
	if code != 200 {
		t.Fatalf("subir: esperaba 200, obtuve %d", code)
	}
	atts := v.Incidents[0].Attachments
	if len(atts) != 1 || atts[0].Name != "error.png" || atts[0].Size != int64(len(pngData)) || atts[0].ContentType != "image/png" {
		t.Fatalf("adjunto mal descrito: %+v", atts)
	}
	last := v.Incidents[0].History[len(v.Incidents[0].History)-1]
	if last.Kind != domain.EvAttached || !strings.Contains(last.Text, "error.png") {
		t.Fatalf("falta el evento en el historial: %+v", last)
	}

	// El agente (que no lo subió) lo descarga: como descarga, con el tipo del servidor y sin que el navegador lo adivine.
	rec := do(h, "GET", "/api/incidents/1/attachments/"+atts[0].ID, tok[2])
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), pngData) {
		t.Fatalf("descargar: %d, %d bytes", rec.Code, rec.Body.Len())
	}
	for k, want := range map[string]string{"Content-Type": "image/png", "X-Content-Type-Options": "nosniff"} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, esperaba %q", k, got, want)
		}
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") || !strings.Contains(cd, "error.png") {
		t.Errorf("Content-Disposition = %q", cd)
	}
}

// Qué se rechaza al subir y con qué código.
func TestAttachmentUploadRules(t *testing.T) {
	h, svc, tok := newServer(t)
	in := domain.NewIncident{Title: "x", Service: "VPN", Priority: "alta", Fields: map[string]string{"sede": "Remoto"}}
	svc.Incidents.Create(1, 3, in) // #1 del solicitante
	svc.Incidents.Create(1, 2, in) // #2 del agente

	for _, c := range []struct {
		name string
		user int
		inc  int
		file string
		data []byte
		want int
	}{
		{"el agente adjunta en un incidente ajeno", 2, 1, "log.txt", []byte("boom"), 200},
		{"el solicitante adjunta en uno ajeno", 3, 2, "log.txt", []byte("boom"), 403},
		{"incidente inexistente", 2, 9, "log.txt", []byte("boom"), 404},
		{"extensión no permitida", 3, 1, "virus.exe", []byte("MZ"), 400},
		{"html disfrazado", 3, 1, "pagina.html", []byte("<script>1</script>"), 400},
		{"png que no es png", 3, 1, "falso.png", []byte("no soy una imagen"), 400},
		{"archivo vacío", 3, 1, "vacio.txt", nil, 400},
		{"más de 5 MB", 3, 1, "grande.txt", bytes.Repeat([]byte("a"), domain.MaxAttachmentSize+1), 400},
	} {
		if got, _ := upload(t, h, tok[c.user], c.inc, c.file, c.data); got != c.want {
			t.Errorf("%s: esperaba %d, obtuve %d", c.name, c.want, got)
		}
	}
	// Sin sesión no se sube ni se descarga.
	if rec := do(h, "POST", "/api/incidents/1/attachments", ""); rec.Code != 401 {
		t.Errorf("subir sin sesión: esperaba 401, obtuve %d", rec.Code)
	}
}

// Quien solo reporta no puede ver los adjuntos de incidentes ajenos; el nombre se limpia de carpetas.
func TestAttachmentVisibilityAndName(t *testing.T) {
	h, svc, tok := newServer(t)
	in := domain.NewIncident{Title: "x", Service: "VPN", Priority: "alta", Fields: map[string]string{"sede": "Remoto"}}
	svc.Incidents.Create(1, 2, in) // #1 del agente

	_, v := upload(t, h, tok[2], 1, `..\..\carpeta/informe.pdf`, pdfData)
	a := v.Incidents[0].Attachments[0]
	if a.Name != "informe.pdf" {
		t.Fatalf("el nombre no se limpió: %q", a.Name)
	}
	if rec := do(h, "GET", "/api/incidents/1/attachments/"+a.ID, tok[3]); rec.Code != 403 {
		t.Errorf("el solicitante descargó un adjunto ajeno: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/incidents/1/attachments/noexiste", tok[2]); rec.Code != 404 {
		t.Errorf("adjunto inexistente: esperaba 404, obtuve %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/incidents/1/attachments/"+a.ID, ""); rec.Code != 401 {
		t.Errorf("descargar sin sesión: esperaba 401, obtuve %d", rec.Code)
	}
}

// Quitar: el solicitante solo los que subió él, el agente cualquiera; en un cerrado no se toca nada.
func TestAttachmentRemoveAndClosed(t *testing.T) {
	h, svc, tok := newServer(t)
	in := domain.NewIncident{Title: "x", Service: "VPN", Priority: "alta", Fields: map[string]string{"sede": "Remoto"}}
	svc.Incidents.Create(1, 3, in) // #1 del solicitante

	_, v := upload(t, h, tok[3], 1, "mio.txt", []byte("mio"))
	mine := v.Incidents[0].Attachments[0]
	_, v = upload(t, h, tok[2], 1, "del-agente.txt", []byte("agente"))
	theirs := v.Incidents[0].Attachments[1]

	if rec := do(h, "DELETE", "/api/incidents/1/attachments/"+theirs.ID, tok[3]); rec.Code != 403 {
		t.Errorf("el solicitante quitó un adjunto del agente: %d", rec.Code)
	}
	if rec := do(h, "DELETE", "/api/incidents/1/attachments/"+mine.ID, tok[3]); rec.Code != 200 {
		t.Errorf("el solicitante no pudo quitar el suyo: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/incidents/1/attachments/"+mine.ID, tok[2]); rec.Code != 404 {
		t.Errorf("el adjunto quitado sigue disponible: %d", rec.Code)
	}

	// Se cierra el incidente: de solo lectura.
	if got, v := call(t, h, tok[2], "/api/incidents/1", `{"status":"Resuelto","resolutionCode":"cancelado","resolutionNote":"x"}`); got != 200 || !v.Incidents[0].Closed() {
		t.Fatalf("no se pudo cerrar el incidente: %d", got)
	}
	if got, _ := upload(t, h, tok[2], 1, "tarde.txt", []byte("tarde")); got != 400 {
		t.Errorf("subir a un cerrado: esperaba 400, obtuve %d", got)
	}
	if rec := do(h, "DELETE", "/api/incidents/1/attachments/"+theirs.ID, tok[2]); rec.Code != 400 {
		t.Errorf("quitar de un cerrado: esperaba 400, obtuve %d", rec.Code)
	}
}

// Un archivo que pasa del tope se rechaza con un mensaje que habla del archivo, y los motivos no arrastran
// el nombre interno del error.
func TestAttachmentErrorMessages(t *testing.T) {
	h, svc, tok := newServer(t)
	in := domain.NewIncident{Title: "x", Service: "VPN", Priority: "alta", Fields: map[string]string{"sede": "Remoto"}}
	svc.Incidents.Create(1, 3, in)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "enorme.txt")
	fw.Write(bytes.Repeat([]byte("a"), 6<<20))
	mw.Close()
	r := httptest.NewRequest("POST", "/api/incidents/1/attachments", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("Authorization", "Bearer "+tok[3])
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "5 MB") {
		t.Fatalf("archivo enorme: %d %s", rec.Code, rec.Body.String())
	}

	var buf bytes.Buffer
	mw = multipart.NewWriter(&buf)
	fw, _ = mw.CreateFormFile("file", "malo.exe")
	fw.Write([]byte("MZ"))
	mw.Close()
	r = httptest.NewRequest("POST", "/api/incidents/1/attachments", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("Authorization", "Bearer "+tok[3])
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 400 || strings.Contains(rec.Body.String(), "adjunto inválido") || !strings.Contains(rec.Body.String(), ".exe") {
		t.Fatalf("extensión prohibida: %d %s", rec.Code, rec.Body.String())
	}
}
