package service

// Errores conocidos típicos de cada rubro: los que casi toda empresa de ese rubro termina teniendo.
// Si en el onboarding se activa la gestión de problemas, la cuenta arranca con ellos y con su workaround.
var knownErrors = map[string][]KnownError{
	"it": {
		{"VPN", "La VPN se cae en horario pico", "El concentrador llega al máximo de sesiones simultáneas que permite la licencia.",
			"Desconectarse y volver a entrar por el gateway secundario."},
		{"Impresoras", "Trabajos trabados en la cola de impresión", "El servicio de cola del servidor de impresión queda colgado después de un trabajo con error.",
			"Reiniciar el servicio de cola desde la consola del servidor, o imprimir por USB mientras tanto."},
		{"Correo", "Los mails a clientes llegan como spam", "Los registros SPF y DKIM del dominio no incluyen a todos los servidores que envían correo.",
			"Mandar lo urgente desde la cuenta de respaldo y avisar por teléfono."},
	},
	"salud": {
		{"Historia clínica electrónica", "La historia clínica no guarda en el cambio de guardia", "Se agotan las conexiones a la base cuando se superponen las sesiones de dos turnos.",
			"Esperar dos minutos y volver a guardar; para consultas urgentes, abrir la historia en modo lectura."},
		{"Turnos online", "El portal de turnos muestra un error de seguridad", "El certificado SSL del portal venció y no se renueva solo.",
			"Dar los turnos por teléfono mientras se renueva el certificado."},
		{"Laboratorio", "Resultados que no aparecen en la historia clínica", "La interfaz con el analizador encola los mensajes cuando se corta la red y no los reenvía.",
			"Reenviar el resultado a mano desde la consola del analizador."},
	},
	"logistica": {
		{"Handhelds de depósito", "Las handhelds no sincronizan los pedidos", "El token de la red Wi-Fi del depósito vence y las terminales no lo renuevan solas.",
			"Reiniciar la handheld cerca de un access point para que pida un token nuevo."},
		{"Balanza y etiquetado", "Etiquetas con el peso cortado", "La plantilla de etiqueta quedó desactualizada después de cambiar el modelo de impresora.",
			"Reimprimir la etiqueta desde la PC de la balanza, que tiene la plantilla correcta."},
	},
	"otro": {
		{"Sistemas", "Se corta internet en la oficina", "El router se cuelga después de un corte de luz y no vuelve a levantar la conexión.",
			"Apagar el router, esperar un minuto y volver a encenderlo."},
	},
}

func init() {
	for k, v := range knownErrors {
		t := templates[k]
		t.KnownErrors = v
		templates[k] = t
	}
}
