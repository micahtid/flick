// Package main is the Flick edge cache service. It answers /health so Docker
// Compose can check it is running.
package main

import (
	"log"
	"net/http"
	"os"
)

// main serves /health on the service's port until the program stops.
func main() {
	port := getenv("PORT", "8082")

	// /health replies "ok". It's a quick way to check the service is running.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	log.Printf("cache listening on :%s", port)
	// ListenAndServe only returns if the server fails. log.Fatal then prints
	// the error and exits.
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

// getenv returns an environment variable, or fallback if it isn't set.
func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
