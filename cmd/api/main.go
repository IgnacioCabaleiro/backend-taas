// TaaS — ticketera configurable como servicio, con gestión de problemas (ITIL 4).
// Este archivo solo arma las piezas: repositorios -> servicios -> handlers -> servidor.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"

	"taas-backend/internal/handler"
	"taas-backend/internal/repository"
	"taas-backend/internal/seed"
	"taas-backend/internal/service"
)

func main() {
	addr := flag.String("addr", ":8080", "dirección en la que escucha el servidor")
	path := flag.String("data", "data.json", "archivo de datos")
	reset := flag.Bool("reset", false, "descarta los datos guardados y vuelve a las cuentas de demo")
	flag.Parse()
	if *reset {
		os.Remove(*path)
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
	svc := service.New(tenants, repository.NewMemorySessions())

	log.Printf("TaaS backend en %s · datos en %s", *addr, *path)
	log.Fatal(http.ListenAndServe(*addr, handler.New(svc)))
}
