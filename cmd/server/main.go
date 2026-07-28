// Command server is the single deployable ZonaryOS backend binary.
package main

import (
	"log"
	"net/http"

	"github.com/moonstreamtech/ZonaryOS/internal/platform/config"
	"github.com/moonstreamtech/ZonaryOS/internal/platform/httpapi"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	mux := httpapi.NewMux()

	log.Printf("ZonaryOS server listening on %s", cfg.HTTPAddr)
	if err := http.ListenAndServe(cfg.HTTPAddr, mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}
