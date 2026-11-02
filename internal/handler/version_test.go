package handler

import "testing"

func TestRestoreConfigVersion(t *testing.T) {
	h, svc, tok := newServer(t)
	view, _ := svc.State.View(1, 1)
	c := view.Config.Clone()
	sla := c.SLAHours["alta"]
	c.SLAHours["alta"] = sla + 1
	if err := svc.Setup.SetConfig(1, 1, c); err != nil {
		t.Fatal(err)
	}

	const v1, v2, v9 = "/api/config/versions/1/restore", "/api/config/versions/2/restore", "/api/config/versions/9/restore"
	for _, c := range []struct {
		token, path string
		want        int
	}{
		{"", v1, 401},     // sin sesión
		{tok[2], v1, 403}, // el agente no configura
		{tok[3], v1, 403}, // el solicitante tampoco
		{tok[1], v9, 404}, // no existe
		{tok[1], v2, 400}, // ya es la vigente
		{tok[1], "/api/config/versions/x/restore", 404},
	} {
		if got, _ := call(t, h, c.token, c.path, ""); got != c.want {
			t.Errorf("%s con %q: esperaba %d, obtuve %d", c.path, c.token, c.want, got)
		}
	}

	code, v := call(t, h, tok[1], v1, "")
	if code != 200 || v.Config.SLAHours["alta"] != sla || len(v.ConfigVersions) != 3 || v.ConfigVersions[2].RestoredFrom != 1 {
		t.Fatalf("restaurar: %d, sla %d, %d versiones", code, v.Config.SLAHours["alta"], len(v.ConfigVersions))
	}
	if view, _ := svc.State.View(1, 2); len(view.ConfigVersions) != 0 {
		t.Fatal("quien no configura no ve el historial")
	}
}
