// Command cacaofermentd runs the CacaoFerment joint-inspection backend and
// serves the embedded browser console. Persistence uses a SQLite WAL database
// under DATA_DIR (default ./data), so tasks, leases, evidence, and credentials
// survive a deterministic restart.
package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"cacaoferment/adapter"
	"cacaoferment/api"
	"cacaoferment/catalog"
	"cacaoferment/evidence"
	"cacaoferment/service"
	"cacaoferment/store"
	"cacaoferment/web"
)

func main() {
	ctx := context.Background()

	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "./data"
	}
	st, err := store.OpenSQLite(ctx, dataDir)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}

	cat := catalog.NewMapCatalog()
	equip := catalog.NewEquipmentDirectory()
	seedCatalog(cat, equip)

	reg := adapter.NewRegistry()
	reg.Register(adapter.NewScriptAdapter(evidence.AdapterProbe, nil))
	reg.Register(adapter.NewScriptAdapter(evidence.AdapterToxinReader, nil))
	reg.Register(adapter.NewScriptAdapter(evidence.AdapterMoistureMeter, nil))
	if !reg.Healthy() {
		log.Fatal("adapter registry is missing a required instrument")
	}

	svc := service.New(st, cat, equip, reg)

	static := web.Handler()
	srv := api.New(svc, static)

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("CacaoFerment listening on %s (data dir %s)", addr, dataDir)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
