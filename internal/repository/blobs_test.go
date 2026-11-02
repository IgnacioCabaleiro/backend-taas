package repository

import (
	"errors"
	"io"
	"path/filepath"
	"testing"

	"taas-backend/internal/domain"
)

func TestDiskBlobs(t *testing.T) {
	dir := t.TempDir()
	b := NewDiskBlobs(dir)
	if err := b.Put("1/2/abc", []byte("hola")); err != nil {
		t.Fatal(err)
	}
	f, err := b.Open("1/2/abc")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(f)
	f.Close()
	if string(got) != "hola" {
		t.Fatalf("leí %q", got)
	}
	if err := b.Delete("1/2/abc"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Open("1/2/abc"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("después de borrar: %v", err)
	}
	if err := b.Delete("1/2/abc"); err != nil {
		t.Fatalf("borrar dos veces no es un error: %v", err)
	}
}

// Una clave que se sale de la carpeta no escribe ni lee fuera de ella.
func TestDiskBlobsRejectsEscapingKeys(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "files")
	b := NewDiskBlobs(dir)
	for _, key := range []string{"../fuera", "a/../../fuera", "", "."} {
		if err := b.Put(key, []byte("x")); !errors.Is(err, domain.ErrInternal) {
			t.Errorf("Put(%q): esperaba ErrInternal, obtuve %v", key, err)
		}
		if _, err := b.Open(key); !errors.Is(err, domain.ErrInternal) {
			t.Errorf("Open(%q): esperaba ErrInternal, obtuve %v", key, err)
		}
	}
}
