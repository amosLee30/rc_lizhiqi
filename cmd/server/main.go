// Command server runs the notification delivery gateway (MVP).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"rc_lizhiqi/internal/adapter"
	"rc_lizhiqi/internal/api"
	"rc_lizhiqi/internal/config"
	"rc_lizhiqi/internal/deliver"
	"rc_lizhiqi/internal/ingest"
	"rc_lizhiqi/internal/mq"
	"rc_lizhiqi/internal/observ"
	"rc_lizhiqi/internal/secret"
	"rc_lizhiqi/internal/store"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg := config.Defaults()
	if v := os.Getenv("ADDR"); v != "" {
		cfg.Addr = v
	}
	suppliers, err := config.LoadSuppliers(os.Getenv("SUPPLIERS_FILE"))
	if err != nil {
		slog.Error("load suppliers", "err", err)
		os.Exit(1)
	}
	cfg.Suppliers = suppliers

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		slog.Error("open store", "err", err)
		os.Exit(1)
	}
	defer func() { _ = st.Close() }()

	resolver, err := secret.NewFileResolver(cfg.SecretsFile)
	if err != nil {
		slog.Error("secret resolver", "err", err)
		os.Exit(1)
	}

	reg := adapter.NewRegistry()
	registerSuppliers(reg, cfg.Suppliers)

	ingestSvc := ingest.New(st, reg, cfg.Suppliers)
	observSvc := observ.New(st)
	bus := mq.NewBus()
	publisher := mq.NewPublisher(st, bus, 500*time.Millisecond, 100)
	worker := deliver.New(st, reg, resolver, cfg)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go worker.Run(ctx)
	go publisher.Run(ctx)
	go logEvents(ctx, bus)

	e := api.New(ingestSvc, observSvc, cfg.OpsToken).Routes()
	go func() {
		slog.Info("listening", "addr", cfg.Addr)
		if err := e.Start(cfg.Addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = e.Shutdown(shutCtx)
}

// registerSuppliers wires one adapter per configured supplier. The adapter kind
// is chosen by config "method"-less convention here for the MVP: types ending
// in "-hmac" use the HMAC adapter, others use the bearer adapter. Real systems
// would map type -> concrete adapter class explicitly.
func registerSuppliers(reg *adapter.Registry, suppliers map[string]config.SupplierConfig) {
	for typ, c := range suppliers {
		method := c.Method
		if method == "" {
			method = http.MethodPost
		}
		if isHMAC(typ) {
			reg.Register(adapter.NewHMACAdapter(typ, c.Endpoint, method, nil))
		} else {
			reg.Register(adapter.NewBearerAdapter(typ, c.Endpoint, method, nil))
		}
	}
}

func isHMAC(typ string) bool {
	return len(typ) >= 5 && typ[len(typ)-5:] == "-hmac"
}

// logEvents is a sample MQ subscriber (stands in for a business consumer).
func logEvents(ctx context.Context, bus *mq.Bus) {
	ch := bus.Subscribe()
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-ch:
			slog.Info("status event", "tracking_id", e.TrackingID, "status", e.Status)
		}
	}
}
