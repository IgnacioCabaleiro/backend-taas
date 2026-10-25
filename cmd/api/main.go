// TaaS — ticketera configurable como servicio, con gestión de problemas (ITIL 4).
// Este archivo solo arma las piezas: repositorios -> servicios -> handlers -> servidor.
package main

import (
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"taas-backend/internal/handler"
	"taas-backend/internal/repository"
	"taas-backend/internal/seed"
	"taas-backend/internal/service"
)

func main() {
	addr := flag.String("addr", ":8080", "dirección en la que escucha el servidor")
	path := flag.String("data", "data.json", "archivo de datos")
	files := flag.String("files", "", "carpeta de los adjuntos (por defecto, «attachments» junto al archivo de datos)")
	reset := flag.Bool("reset", false, "descarta los datos guardados y vuelve a las cuentas de demo")
	flag.Parse()
	defaultFiles := *files == ""
	if defaultFiles {
		*files = filepath.Join(filepath.Dir(*path), "attachments")
	}
	if *reset {
		if err := os.Remove(*path); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Fatal(err)
		}
		// Los adjuntos de las cuentas descartadas quedarían sueltos. Solo se borra la carpeta por defecto:
		// una indicada con -files la administra quien la pasó.
		if defaultFiles {
			if err := os.RemoveAll(*files); err != nil {
				log.Fatal(err)
			}
		}
	}

	tenants, err := repository.NewJSONTenants(*path)
	if err != nil {
		log.Fatal(err)
	}
	if tenants.Empty() { // primera vez: cuentas de demo
		for _, t := range seed.Tenants() {
			if err := tenants.Create(t); err != nil {
				log.Fatal(err)
			}
		}
	}
	svc := service.New(tenants, repository.NewMemorySessions(), repository.NewDiskBlobs(*files))

	log.Printf("TaaS backend en %s · datos en %s", *addr, *path)
	// Timeouts: para evitar cuelges, validar si estan bien los tiempos
	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler.New(svc),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}
