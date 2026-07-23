// Copyright (C) 2026 Jon Shaulis
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

// Command billtracker is the Bill Tracker hhq plugin - a persistent,
// Postgres-backed external-process plugin implementing hhq's plugin
// contract (see github.com/mscreations/happyhome-quest/internal/plugins on
// the hhq side):
//
//	GET  /manifest  - static metadata: kiosk nav/view shape, whether events are provided
//	GET  /view      - a full-screen HTML page inlined into the kiosk's content region
//	GET  /events    - synthetic calendar events for a date window (JSON)
//	GET  /settings  - a full HTML settings page, proxied through hhq's own
//	POST /settings  -   parent-authenticated dashboard (see ProxySettings)
//	GET  /healthz   - liveness check
//
// Bills are defined either via CONFIG_DIR/bills.json (bootstrap, reconciled
// on every startup) or the settings page - both persist to this plugin's
// own bt_-prefixed Postgres tables. Bank/card balances are optionally
// synced from a SimpleFIN Bridge connection, configured entirely from the
// settings page. See README.md for env vars and setup.
package main

import (
	"context"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/mscreations/billtracker-plugin/internal/config"
	_ "github.com/mscreations/billtracker-plugin/internal/connectors/billeriq"
	_ "github.com/mscreations/billtracker-plugin/internal/connectors/columbiagaspa"
	"github.com/mscreations/billtracker-plugin/internal/db"
	"github.com/mscreations/billtracker-plugin/internal/handlers"
	"github.com/mscreations/billtracker-plugin/internal/logging"
	"github.com/mscreations/billtracker-plugin/internal/models"
	"github.com/mscreations/billtracker-plugin/internal/scheduler"
	"github.com/mscreations/billtracker-plugin/internal/util"
	"github.com/mscreations/billtracker-plugin/web"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}

	conn, err := db.New(cfg.DSN())
	if err != nil {
		log.Fatalf("connecting to db: %v", err)
	}
	defer conn.Close()

	if err := db.Migrate(conn); err != nil {
		log.Fatalf("running migrations: %v", err)
	}

	encryptor, err := util.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		log.Fatalf("initializing encryptor: %v", err)
	}

	tmpl, err := template.ParseFS(web.TemplatesFS, "templates/*.html", "templates/*/*.html")
	if err != nil {
		log.Fatalf("parsing templates: %v", err)
	}

	app := &handlers.App{
		Cfg:       cfg,
		BillDefs:  &models.BillDefinitionStore{DB: conn},
		Instances: &models.BillInstanceStore{DB: conn},
		Accounts:  &models.AccountStore{DB: conn},
		Settings:  &models.SettingsStore{DB: conn},
		SimpleFin: &models.SimpleFinConnectionStore{DB: conn},
		Vendors:   &models.VendorConnectionStore{DB: conn},
		Encryptor: encryptor,
		Templates: tmpl,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bootstrapBills(ctx, app, cfg)

	sched := &scheduler.Scheduler{
		Cfg:       cfg,
		BillDefs:  app.BillDefs,
		Instances: app.Instances,
		Accounts:  app.Accounts,
		SimpleFin: app.SimpleFin,
		Vendors:   app.Vendors,
		Encryptor: app.Encryptor,
	}
	go sched.Run(ctx)

	// Every route except /register and /healthz requires hhq's shared bearer
	// token (see app.RequireBearerToken). /register is how that token gets
	// issued in the first place - see internal/handlers/register.go, and
	// this repo's CLAUDE.md for the full self-registration flow. /healthz
	// stays open since it's polled by Kubernetes' own liveness probe, which
	// sends no auth header.
	auth := app.RequireBearerToken

	mux := http.NewServeMux()
	mux.HandleFunc("POST /register", app.Register)
	mux.HandleFunc("GET /manifest", auth(app.Manifest))
	mux.HandleFunc("GET /view", auth(app.View))
	mux.HandleFunc("GET /events", auth(app.Events))
	mux.HandleFunc("POST /actions/{id}", auth(app.Action))
	mux.HandleFunc("GET /settings", auth(app.SettingsPage))
	mux.HandleFunc("POST /settings", auth(app.SettingsPage))
	mux.HandleFunc("GET /healthz", app.Healthz)

	srv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: mux,
	}

	go func() {
		logging.Infof("Bill Tracker plugin listening on %s", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	logging.Infof("shutting down")
	cancel()
	_ = srv.Shutdown(context.Background())
}

// bootstrapBills reads CONFIG_DIR/bills.json, if present, and reconciles it
// against the database (see handlers.App.BootstrapBills). A missing file is
// not an error - bills.json is optional, the settings UI works without it.
func bootstrapBills(ctx context.Context, app *handlers.App, cfg *config.Config) {
	path := filepath.Join(cfg.ConfigDir, "bills.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			logging.Infof("no bills.json at %s - skipping bootstrap", path)
			return
		}
		logging.Errorf("reading %s: %v", path, err)
		return
	}

	entries, err := config.ParseBillsBootstrap(string(raw))
	if err != nil {
		logging.Errorf("parsing %s: %v", path, err)
		return
	}
	app.BootstrapBills(ctx, entries)
	logging.Infof("bootstrapped %d bill(s) from %s", len(entries), path)
}
