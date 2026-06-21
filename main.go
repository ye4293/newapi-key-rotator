package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)

	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	store, err := NewStore(cfg.DataDir)
	if err != nil {
		log.Fatalf("store error: %v", err)
	}

	client := NewClient(cfg)
	rotator := NewRotator(cfg, client, store)

	trigger := make(chan struct{}, 1)
	server := NewServer(cfg, store, rotator, trigger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Background failover loop.
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		rotator.Run(ctx, trigger)
	}()

	// Web console.
	httpSrv := &http.Server{
		Addr:              cfg.WebListen,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Printf("INFO web console listening on %s (channel #%d, poll %s)", cfg.WebListen, cfg.ChannelID, cfg.PollInterval)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("web server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("INFO shutting down…")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	<-loopDone
	_ = os.Stdout.Sync()
}
