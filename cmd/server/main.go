package main

import (
	"log"
	"m365-native/internal/outbound"
	"m365-native/internal/web"
	"net/http"
	"os"
	"time"
)

func main() {
	web.ApplyStartupSettingsEnv()
	if err := outbound.ConfigureFromEnv(); err != nil {
		log.Fatalf("configure outbound proxy: %v", err)
	}
	s, e := web.New()
	if e != nil {
		log.Fatal(e)
	}
	listen := "127.0.0.1:4141"
	if v := os.Getenv("M365_LISTEN"); v != "" {
		listen = v
	}
	log.Printf("m365-native listening on http://%s\\n", listen)
	server := &http.Server{
		Addr:              listen,
		Handler:           s.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		WriteTimeout:      0, // streaming endpoints need an open-ended write window.
	}
	log.Fatal(server.ListenAndServe())
}
