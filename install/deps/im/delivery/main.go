// The Airway IM delivery worker implements the transactional-outbox pattern:
// it polls the backend for committed outbox events, pushes each one to the
// WebSocket gateway, and acknowledges it only after the gateway accepted the
// delivery command. Messages are never lost between the durable commit and
// the real-time push; duplicates are possible and clients deduplicate by
// event/message ID. See deps/im/docs/design/delivery.md for the full semantics.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

type config struct {
	addr           string
	backendURL     string
	gatewayURL     string
	internalSecret string
	pollInterval   time.Duration
}

type outboxEvent struct {
	ID      string         `json:"id"`
	Topic   string         `json:"topic"`
	Payload map[string]any `json:"payload"`
}

type stats struct {
	polled     atomic.Int64
	published  atomic.Int64
	failures   atomic.Int64
	lastUnix   atomic.Int64
	inProgress atomic.Bool
}

type worker struct {
	config config
	client *http.Client
	stats  stats
}

func main() {
	cfg := loadConfig()
	w := &worker{config: cfg, client: &http.Client{Timeout: 5 * time.Second}}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(response http.ResponseWriter, _ *http.Request) {
		writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/metrics", w.metrics)
	mux.HandleFunc("/dashboard", w.dashboard)
	server := &http.Server{Addr: cfg.addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	go w.run(ctx)
	go func() {
		log.Printf("Airway IM delivery listening on %s", cfg.addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	_ = server.Shutdown(shutdown)
}

func (w *worker) run(ctx context.Context) {
	ticker := time.NewTicker(w.config.pollInterval)
	defer ticker.Stop()
	for {
		if err := w.poll(ctx); err != nil && !errors.Is(err, context.Canceled) {
			w.stats.failures.Add(1)
			log.Printf("delivery poll failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *worker) poll(ctx context.Context) error {
	w.stats.inProgress.Store(true)
	defer w.stats.inProgress.Store(false)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, w.config.backendURL+"/internal/v1/outbox?limit=100", nil)
	req.Header.Set("X-IM-Internal-Secret", w.config.internalSecret)
	response, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("backend returned %s", response.Status)
	}
	var body struct {
		Data []outboxEvent `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		return err
	}
	w.stats.polled.Add(int64(len(body.Data)))
	for _, event := range body.Data {
		if err := w.publish(ctx, event); err != nil {
			return err
		}
		if err := w.ack(ctx, event.ID); err != nil {
			return err
		}
		w.stats.published.Add(1)
		w.stats.lastUnix.Store(time.Now().Unix())
	}
	return nil
}

func (w *worker) publish(ctx context.Context, event outboxEvent) error {
	body, _ := json.Marshal(map[string]any{"id": event.ID, "topic": event.Topic, "payload": event.Payload})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, w.config.gatewayURL+"/internal/v1/deliver", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-IM-Internal-Secret", w.config.internalSecret)
	response, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("gateway returned %s", response.Status)
	}
	return nil
}

func (w *worker) ack(ctx context.Context, id string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, w.config.backendURL+"/internal/v1/outbox/"+id+"/ack", nil)
	req.Header.Set("X-IM-Internal-Secret", w.config.internalSecret)
	response, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("outbox ack returned %s", response.Status)
	}
	return nil
}

func (w *worker) metrics(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(response, "airway_im_delivery_events_polled_total %d\nairway_im_delivery_events_published_total %d\nairway_im_delivery_failures_total %d\nairway_im_delivery_last_success_unixtime %d\n", w.stats.polled.Load(), w.stats.published.Load(), w.stats.failures.Load(), w.stats.lastUnix.Load())
}

func (w *worker) dashboard(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(response, `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="refresh" content="5"><title>Airway IM Delivery</title><style>body{font:16px system-ui;margin:3rem;background:#111;color:#eee}pre{background:#222;padding:1rem;border-radius:8px}</style></head><body><h1>Airway IM Delivery</h1><p>Transactional outbox publisher. Refreshes every 5 seconds.</p><pre>`)
	w.metrics(response, nil)
	fmt.Fprint(response, "</pre></body></html>")
}

func loadConfig() config {
	intervalMS, _ := strconv.Atoi(envOr("DELIVERY_POLL_INTERVAL_MS", "500"))
	if intervalMS < 100 {
		intervalMS = 100
	}
	return config{addr: envOr("DELIVERY_ADDR", ":1920"), backendURL: strings.TrimRight(envOr("INTERNAL_SERVICE_URL", "http://127.0.0.1:1906"), "/"), gatewayURL: strings.TrimRight(envOr("GATEWAY_URL", "http://127.0.0.1:1910"), "/"), internalSecret: os.Getenv("IM_INTERNAL_SECRET"), pollInterval: time.Duration(intervalMS) * time.Millisecond}
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
