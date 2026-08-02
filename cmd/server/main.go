package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ratewatch/internal/config"
	"ratewatch/internal/connectors"
	"ratewatch/internal/httpapi"
	"ratewatch/internal/security"
	"ratewatch/internal/store"
	"ratewatch/internal/syncer"
	"ratewatch/internal/updater"
)

func main() {
	if updater.RunHelper(os.Args) {
		return
	}
	go updater.CleanupHelpers()
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	st, err := store.Open(cfg.DatabasePath)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()
	adminHash, err := security.HashPassword("123456")
	if err != nil {
		log.Fatal(err)
	}
	if err = st.EnsureAdmin("tokenav", adminHash); err != nil {
		log.Fatal(err)
	}
	vault, err := security.NewVault(cfg.MasterKey)
	if err != nil {
		log.Fatal(err)
	}
	client := connectors.New()
	hub := syncer.NewHub()
	engine := syncer.New(st, vault, client, hub, cfg)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	engine.Start(ctx)
	api := httpapi.New(cfg, st, vault, client, engine, hub)
	api.SetRestart(cancel)
	srv := &http.Server{Addr: cfg.Addr, Handler: api.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	go func() {
		<-ctx.Done()
		shutdown, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelShutdown()
		_ = srv.Shutdown(shutdown)
	}()
	log.Printf("倍率同步平台已启动: %s", cfg.Addr)
	if err = srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
