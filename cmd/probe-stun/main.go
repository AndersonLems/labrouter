package main

import (
	"context"
	"encoding/json"
	"fmt"
	"lab-router/internal/model"
	"lab-router/internal/store"
	"lab-router/internal/traversal"
	"os"
	"path/filepath"
	"time"
)

func main() {
	mode := "rfc3489"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	dir, e := os.MkdirTemp("artifacts", "stun-probe-")
	if e != nil {
		panic(e)
	}
	defer os.RemoveAll(dir)
	s, e := store.Open(filepath.Join(dir, "state"))
	if e != nil {
		panic(e)
	}
	defer s.DB.Close()
	m := traversal.New(s, func(string, string) (string, error) { return "", fmt.Errorf("probe somente; pedidos não aceitos") })
	c := model.Default().CWMP
	c.STUNMode = mode
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	status, e := m.ProbeSTUN(ctx, c)
	out := map[string]any{"mode": mode, "status": status, "scope": "binding temporário; não testa entrega UDP CR nem habilita receptor"}
	if e != nil {
		out["error"] = e.Error()
	}
	json.NewEncoder(os.Stdout).Encode(out)
}
