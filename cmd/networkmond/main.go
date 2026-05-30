package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/fujr/connection_recorder/internal/api"
	"github.com/fujr/connection_recorder/internal/collector"
	"github.com/fujr/connection_recorder/internal/config"
	"github.com/fujr/connection_recorder/internal/store"
)

type daemon struct {
	mu          sync.RWMutex
	storeMu     sync.RWMutex
	cfg         config.Config
	store       *store.Store
	collector   *collector.Collector
	lastCollect time.Time
	lastErr     string
	lastCount   int
}

func main() {
	cfg := config.Default()
	flag.StringVar(&cfg.DBPath, "db", cfg.DBPath, "SQLite database path")
	flag.StringVar(&cfg.Socket, "socket", cfg.Socket, "Unix socket path")
	interval := flag.String("interval", cfg.Interval.String(), "poll interval")
	retention := flag.String("retention", cfg.Retention.String(), "record retention")
	flag.Parse()

	var err error
	cfg.Interval, err = time.ParseDuration(*interval)
	if err != nil {
		log.Fatalf("invalid interval: %v", err)
	}
	cfg.Retention, err = time.ParseDuration(*retention)
	if err != nil {
		log.Fatalf("invalid retention: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config: %v", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}

	d := &daemon{
		cfg:       cfg,
		store:     st,
		collector: collector.New(),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		d.collectLoop(ctx)
	}()
	go func() {
		defer wg.Done()
		if err := d.serve(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("api server stopped: %v", err)
		}
	}()
	log.Printf("networkmond started db=%s socket=%s interval=%s retention=%s", cfg.DBPath, cfg.Socket, cfg.Interval, cfg.Retention)
	wg.Wait()
	d.storeMu.Lock()
	_ = d.store.Close()
	d.storeMu.Unlock()
	log.Printf("networkmond stopped")
}

func (d *daemon) collectLoop(ctx context.Context) {
	pruneTicker := time.NewTicker(time.Minute)
	defer pruneTicker.Stop()

	for {
		d.collectOnce(ctx)
		d.mu.RLock()
		interval := d.cfg.Interval
		d.mu.RUnlock()

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		case <-pruneTicker.C:
			d.prune(ctx)
		}
	}
}

func (d *daemon) collectOnce(ctx context.Context) {
	now := time.Now()
	conns, err := d.collector.Snapshot(now)
	if err == nil {
		d.storeMu.RLock()
		err = d.store.UpsertConnections(ctx, conns)
		d.storeMu.RUnlock()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lastCollect = now
	d.lastCount = len(conns)
	if err != nil {
		d.lastErr = err.Error()
		log.Printf("collect failed: %v", err)
		return
	}
	d.lastErr = ""
}

func (d *daemon) prune(ctx context.Context) {
	d.mu.RLock()
	retention := d.cfg.Retention
	d.mu.RUnlock()
	d.storeMu.RLock()
	_, err := d.store.Prune(ctx, retention)
	d.storeMu.RUnlock()
	if err != nil {
		log.Printf("prune failed: %v", err)
	}
}

func (d *daemon) serve(ctx context.Context) error {
	d.mu.RLock()
	socketPath := d.cfg.Socket
	d.mu.RUnlock()
	if err := os.MkdirAll(filepath.Dir(socketPath), 0755); err != nil {
		return err
	}
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(socketPath)
	if err := os.Chmod(socketPath, 0660); err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return context.Canceled
			}
			return err
		}
		go d.handle(conn)
	}
}

func (d *daemon) handle(conn net.Conn) {
	defer conn.Close()
	var req api.Request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		writeResponse(conn, api.Response{OK: false, Error: err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	switch req.Command {
	case "status":
		status, err := d.status(ctx)
		if err != nil {
			writeResponse(conn, api.Response{OK: false, Error: err.Error()})
			return
		}
		writeResponse(conn, api.Response{OK: true, Status: &status})
	case "list":
		q, err := api.ParseQuery(req)
		if err != nil {
			writeResponse(conn, api.Response{OK: false, Error: err.Error()})
			return
		}
		d.storeMu.RLock()
		records, err := d.store.List(ctx, q)
		d.storeMu.RUnlock()
		if err != nil {
			writeResponse(conn, api.Response{OK: false, Error: err.Error()})
			return
		}
		writeResponse(conn, api.Response{OK: true, Records: records})
	case "config_get":
		d.mu.RLock()
		cfg := d.cfg
		d.mu.RUnlock()
		writeResponse(conn, api.Response{OK: true, Config: cfg.Wire()})
	case "config_set":
		if err := d.setConfig(req); err != nil {
			writeResponse(conn, api.Response{OK: false, Error: err.Error()})
			return
		}
		d.mu.RLock()
		cfg := d.cfg
		d.mu.RUnlock()
		writeResponse(conn, api.Response{OK: true, Config: cfg.Wire()})
	case "prune":
		d.mu.RLock()
		retention := d.cfg.Retention
		d.mu.RUnlock()
		d.storeMu.RLock()
		pruned, err := d.store.Prune(ctx, retention)
		d.storeMu.RUnlock()
		if err != nil {
			writeResponse(conn, api.Response{OK: false, Error: err.Error()})
			return
		}
		writeResponse(conn, api.Response{OK: true, Pruned: pruned})
	default:
		writeResponse(conn, api.Response{OK: false, Error: fmt.Sprintf("unknown command %q", req.Command)})
	}
}

func (d *daemon) status(ctx context.Context) (api.Status, error) {
	d.mu.RLock()
	cfg := d.cfg
	lastCollect := d.lastCollect
	lastErr := d.lastErr
	lastCount := d.lastCount
	d.mu.RUnlock()
	d.storeMu.RLock()
	stats, err := d.store.Stats(ctx, cfg.DBPath)
	d.storeMu.RUnlock()
	if err != nil {
		return api.Status{}, err
	}
	return api.Status{
		Config:          cfg.Wire(),
		Stats:           stats,
		LastCollectTime: lastCollect.Format(time.RFC3339),
		LastCollectUnix: lastCollect.Unix(),
		LastCollectErr:  lastErr,
		LastCount:       lastCount,
	}, nil
}

func (d *daemon) setConfig(req api.Request) error {
	var oldStore *store.Store
	var nextStore *store.Store

	d.mu.RLock()
	next := d.cfg
	d.mu.RUnlock()

	if req.Interval != "" {
		interval, err := time.ParseDuration(req.Interval)
		if err != nil {
			return err
		}
		next.Interval = interval
	}
	if req.Retention != "" {
		retention, err := time.ParseDuration(req.Retention)
		if err != nil {
			return err
		}
		next.Retention = retention
	}
	if req.DBPath != "" {
		next.DBPath = req.DBPath
	}
	if err := next.Validate(); err != nil {
		return err
	}
	d.mu.RLock()
	currentDB := d.cfg.DBPath
	d.mu.RUnlock()
	if next.DBPath != currentDB {
		var err error
		nextStore, err = store.Open(next.DBPath)
		if err != nil {
			return err
		}
		d.storeMu.Lock()
		oldStore = d.store
		d.store = nextStore
		d.storeMu.Unlock()
		if oldStore != nil {
			_ = oldStore.Close()
		}
	}
	d.mu.Lock()
	d.cfg = next
	d.mu.Unlock()
	return nil
}

func writeResponse(conn net.Conn, resp api.Response) {
	_ = json.NewEncoder(conn).Encode(resp)
}
