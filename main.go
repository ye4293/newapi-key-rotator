package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

type instance struct {
	cfg     *InstanceConfig
	store   *Store
	rotator *Rotator
	trigger chan struct{}
}

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)

	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	instances := make([]*instance, 0, len(cfg.Instances))
	for i, instCfg := range cfg.Instances {
		poolFile := fmt.Sprintf("pool_%d.json", i)
		if i == 0 {
			poolFile = "pool.json"
		}
		store, err := NewStore(filepath.Join(cfg.DataDir, poolFile))
		if err != nil {
			log.Fatalf("store error (instance %d): %v", i, err)
		}
		if store.IsDeleted() {
			log.Printf("INFO instance %d (channel #%d) is deleted — skipping", i, instCfg.ChannelIDs[0])
			continue
		}
		client := NewClient(instCfg, cfg)
		trigger := make(chan struct{}, 1)
		tmpLabel := fmt.Sprintf("ch-%d", instCfg.ChannelIDs[0])
		rotator := NewRotator(tmpLabel, instCfg, cfg, client, store)
		instances = append(instances, &instance{cfg: instCfg, store: store, rotator: rotator, trigger: trigger})
	}

	server := NewServer(cfg, instances)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	doneChs := make([]chan struct{}, len(instances))
	for i, inst := range instances {
		done := make(chan struct{})
		doneChs[i] = done
		go func(inst *instance, done chan struct{}) {
			defer close(done)
			inst.rotator.Run(ctx, inst.trigger)
		}(inst, done)
	}

	httpSrv := &http.Server{
		Addr:              cfg.WebListen,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Printf("INFO web console listening on %s (%d instance(s), poll %s)", cfg.WebListen, len(instances), cfg.PollInterval)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("web server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("INFO shutting down…")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	for _, done := range doneChs {
		<-done
	}
	_ = os.Stdout.Sync()
}
