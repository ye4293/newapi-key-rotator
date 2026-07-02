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
	instIdx   int // 对应 cfg.Instances 的下标（0-based）
	channelID int // 该 instance 监控的渠道 ID
	cfg       *InstanceConfig
	store     *Store
	rotator   *Rotator
	trigger   chan struct{}
}

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)

	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	instances := make([]*instance, 0)
	for i, instCfg := range cfg.Instances {
		platformLabel := instCfg.Platform
		if platformLabel == "" {
			platformLabel = fmt.Sprintf("inst-%d", i)
		}
		for _, chID := range instCfg.ChannelIDs {
			label := fmt.Sprintf("%s/ch-%d", platformLabel, chID)
			poolFile := fmt.Sprintf("pool-%d-%d.json", i, chID)
			store, err := NewStore(filepath.Join(cfg.DataDir, poolFile))
			if err != nil {
				log.Fatalf("store error (%s): %v", label, err)
			}
			if store.IsDeleted() {
				log.Printf("INFO [%s] is deleted — skipping", label)
				continue
			}
			client := NewClient(instCfg, cfg)
			trigger := make(chan struct{}, 1)
			rotator := NewRotator(label, instCfg, cfg, client, store)
			instances = append(instances, &instance{
				instIdx:   i,
				channelID: chID,
				cfg:       instCfg,
				store:     store,
				rotator:   rotator,
				trigger:   trigger,
			})
		}
	}

	if len(instances) == 0 {
		log.Fatalf("no active monitoring instances — check your channel configuration")
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
