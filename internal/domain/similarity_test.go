package domain

import (
	"reflect"
	"testing"
)

func TestTerms(t *testing.T) {
	got := Terms("¡Las IMPRESORAS del 2° piso no funcionan! Error en la impresión")
	want := []string{"impresora", "piso", "impresion"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("esperaba %v, obtuve %v", want, got)
	}
}

func TestRank(t *testing.T) {
	docs := map[int]string{
		1: "Caídas intermitentes de la VPN. La VPN se desconecta cada 10 minutos",
		2: "La impresora del segundo piso no imprime",
		3: "El ERP tarda en facturar",
	}
	got := rank("se me desconecta la vpn", docs, nil, 3)
	if len(got) != 1 || got[0].ID != 1 || got[0].Shared[0] != "desconecta" {
		t.Fatalf("solo la VPN se parece: %+v", got)
	}
	if got := rank("hola", docs, nil, 3); len(got) != 0 {
		t.Fatalf("un texto sin palabras útiles no sugiere nada: %+v", got)
	}
	// El bonus de servicio desempata, pero no inventa coincidencias.
	got = rank("no anda el sistema", map[int]string{1: "sistema de ruteo", 2: "sistema de ventas"}, map[int]float64{2: ServiceBonus}, 3)
	if len(got) != 2 || got[0].ID != 2 {
		t.Fatalf("a igual texto, gana el del mismo servicio: %+v", got)
	}
	if got := rank("teclado", map[int]string{1: "mouse"}, map[int]float64{1: 1}, 3); len(got) != 0 {
		t.Fatalf("sin palabras en común no hay coincidencia, aunque haya bonus: %+v", got)
	}
}
