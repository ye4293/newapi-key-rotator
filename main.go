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

	instances := make([]*instance, len(cfg.Instances))
	for i, instCfg := range cfg.Instances {
		poolFile := fmt.Sprintf("pool_%d.json", i)
		if i == 0 {
			poolFile = "pool.json"
		}
		store, err := NewStore(filepath.Join(cfg.DataDir, poolFile))
		if err != nil {
			log.Fatalf("store error (instance %d): %v", i, err)
		}
		client := NewClient(instCfg, cfg)
		trigger := make(chan struct{}, 1)
		rotator := NewRotator(instCfg, cfg, client, store)
		instances[i] = &instance{cfg: instCfg, store: store, rotator: rotator, trigger: trigger}
	}

	server := NewServer(cfg, instances)

	// Wire cross-instance sync: when any instance rotates, all siblings immediately
	// apply the same key to their channels so all pools stay in step.
	for i, inst := range instances {
		siblings := make([]*instance, 0, len(instances)-1)
		for j, other := range instances {
			if j != i {
				siblings = append(siblings, other)
			}
		}
		inst.rotator.SetOnRotate(func(newIdx int) {
			syncCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTPTimeout)
			defer cancel()
			for _, sib := range siblings {
				sib.rotator.SyncToIndex(syncCtx, newIdx)
			}
		})
	}

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
