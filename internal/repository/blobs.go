package repository

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"taas-backend/internal/domain"
)

// Blobs guarda el contenido de los adjuntos, aparte de las cuentas. Las claves las arma el servicio
// ("cuenta/incidente/id"): hoy una carpeta en disco, mañana un bucket.
type Blobs interface {
	Put(key string, data []byte) error
	Open(key string) (io.ReadCloser, error)
	Delete(key string) error
}

// DiskBlobs guarda cada adjunto como un archivo bajo dir.
type DiskBlobs struct{ dir string }

func NewDiskBlobs(dir string) *DiskBlobs { return &DiskBlobs{dir: dir} }

// path resuelve la clave dentro de dir; una clave que se salga de la carpeta se rechaza.
func (d *DiskBlobs) path(key string) (string, error) {
	p := filepath.Join(d.dir, filepath.FromSlash(key))
	if rel, err := filepath.Rel(d.dir, p); err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("clave inválida %q: %w", key, domain.ErrInternal)
	}
	return p, nil
}

func (d *DiskBlobs) Put(key string, data []byte) error {
	p, err := d.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	// Se escribe a un temporal y se renombra: un corte a mitad de camino no deja un archivo a medias.
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (d *DiskBlobs) Open(key string) (io.ReadCloser, error) {
	p, err := d.path(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("adjunto: %w", domain.ErrNotFound)
	}
	return f, err
}

func (d *DiskBlobs) Delete(key string) error {
	p, err := d.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// MemoryBlobs guarda los adjuntos en memoria (tests).
type MemoryBlobs struct {
	mu    sync.Mutex
	files map[string][]byte
}

func NewMemoryBlobs() *MemoryBlobs { return &MemoryBlobs{files: map[string][]byte{}} }

func (m *MemoryBlobs) Put(key string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[key] = append([]byte(nil), data...)
	return nil
}

func (m *MemoryBlobs) Open(key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.files[key]
	if !ok {
		return nil, fmt.Errorf("adjunto: %w", domain.ErrNotFound)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *MemoryBlobs) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, key)
	return nil
}

// Len: cuántos archivos hay guardados (para los tests).
func (m *MemoryBlobs) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.files)
}
