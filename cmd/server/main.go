package main

import (
	"log"
	"m365-native/internal/web"
	"net/http"
	"os"
)

func main() {
	web.ApplyStartupSettingsEnv()
	s, e := web.New()
	if e != nil {
		log.Fatal(e)
	}
	listen := "127.0.0.1:4141"
	if v := os.Getenv("M365_LISTEN"); v != "" {
		listen = v
	}
	log.Printf("m365-native listening on http://%s\\n", listen)
	log.Fatal(http.ListenAndServe(listen, s.Routes()))
}
