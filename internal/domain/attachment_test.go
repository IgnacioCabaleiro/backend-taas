package domain

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestValidateAttachment(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")
	for _, c := range []struct {
		name, file string
		data       []byte
		wantName   string
		wantType   string
		bad        bool
	}{
		{"imagen", "a.PNG", png, "a.PNG", "image/png", false},
		{"pdf", "doc.pdf", []byte("%PDF-1.7"), "doc.pdf", "application/pdf", false},
		{"texto", "run.log", []byte("linea 1\nlinea 2"), "run.log", "text/plain; charset=utf-8", false},
		{"carpetas fuera", `C:\Users\x\..\a.txt`, []byte("x"), "a.txt", "text/plain; charset=utf-8", false},
		{"extensión prohibida", "a.exe", []byte("MZ"), "", "", true},
		{"sin extensión", "README", []byte("x"), "", "", true},
		{"sin nombre", "///", []byte("x"), "", "", true},
		{"vacío", "a.txt", nil, "", "", true},
		{"png falso", "a.png", []byte("texto"), "", "", true},
		{"pdf falso", "a.pdf", []byte("texto"), "", "", true},
		{"texto con bytes nulos", "a.txt", []byte("a\x00b"), "", "", true},
		{"muy grande", "a.txt", bytes.Repeat([]byte("a"), MaxAttachmentSize+1), "", "", true},
	} {
		name, typ, err := ValidateAttachment(c.file, c.data)
		switch {
		case c.bad && !errors.Is(err, ErrAttachment):
			t.Errorf("%s: esperaba ErrAttachment, obtuve %v", c.name, err)
		case !c.bad && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case !c.bad && (name != c.wantName || typ != c.wantType):
			t.Errorf("%s: obtuve %q (%s), esperaba %q (%s)", c.name, name, typ, c.wantName, c.wantType)
		}
	}
}

func TestCleanFileNameKeepsExtension(t *testing.T) {
	got := CleanFileName(strings.Repeat("a", 300) + ".pdf")
	if len([]rune(got)) != 100 || !strings.HasSuffix(got, ".pdf") {
		t.Fatalf("nombre largo mal recortado: %d caracteres, termina en %q", len([]rune(got)), got[len(got)-4:])
	}
	if got := CleanFileName("a\x00b\x1b.txt"); got != "ab.txt" {
		t.Fatalf("caracteres de control: %q", got)
	}
}
