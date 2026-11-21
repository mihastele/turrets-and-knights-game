// Lane Siege — a head-to-head lane siege game.
//
// Usage:
//
//	go run ./cmd/lanesiege [-addr :8080] [-web web]
package main

import (
	"flag"
	"log"

	"lanesiege/internal/server"
)

func main() {
	addr := flag.String("addr", ":8080", "address to listen on")
	web := flag.String("web", "web", "path to the web client directory")
	flag.Parse()

	srv := server.New(*web)
	defer srv.Close()

	log.Printf("Lane Siege listening on http://localhost%s", *addr)
	log.Printf("Open two browser tabs, create a room in one and join it from the other.")
	if err := srv.ListenAndServe(*addr); err != nil {
		log.Fatal(err)
	}
}
