package main

import (
	"context"
	"errors"
	"log"
	"m365-copilot2api/internal/outbound"
	"m365-copilot2api/internal/web"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	if exe, err := os.Executable(); err == nil {
		if dir := filepath.Dir(exe); dir != "" {
			os.Chdir(dir)
		}
	}
	web.ApplyStartupSettingsEnv()
	if err := outbound.ConfigureFromEnv(); err != nil {
		log.Fatalf("configure outbound proxy: %v", err)
	}
	s, e := web.New()
	if e != nil {
		log.Fatal(e)
	}
	s.InitM365CloudClient()
	s.StartAutoCleanup()
	s.StartConvCacheGC()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s.StartTokenPreRefresh(ctx)
	listen := "127.0.0.1:4141"
	if v := os.Getenv("M365_LISTEN"); v != "" {
		listen = v
	}
	log.Printf("m365-copilot2api listening on http://%s\\n", listen)
	server := &http.Server{
		Addr:              listen,
		Handler:           s.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		WriteTimeout:      0, // streaming endpoints need an open-ended write window.
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("graceful shutdown: %v", err)
		}
	}()
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	web.StopPersistLoop()
	log.Println("shutdown complete")
}
