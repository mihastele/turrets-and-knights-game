// Lane Siege — a head-to-head lane siege game.
//
// The game client is embedded into the binary, so a single static binary is
// all you need to host it:
//
//	go run ./cmd/lanesiege
//	docker build -t lanesiege . && docker run -p 8180:8180 lanesiege
//
// Flags / environment:
//
//	-addr   listen address (default: ":8180", or ":"+$PORT when PORT is set,
//	        e.g. on Fly.io / Render / Heroku-style platforms)
//	-web    serve the client from this directory instead of the embedded FS
//	        (useful for editing the client without recompiling)
package main

import (
	"context"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	lanesiege "lanesiege"
	"lanesiege/internal/server"
)

func main() {
	addr := flag.String("addr", "", "listen address (overrides $PORT; default :8180)")
	web := flag.String("web", "", "serve client from this directory instead of the embedded assets")
	flag.Parse()

	listen := *addr
	if listen == "" {
		if p := os.Getenv("PORT"); p != "" {
			listen = ":" + p
		} else {
			listen = ":8180"
		}
	}

	var webFS fs.FS
	if *web != "" {
		webFS = os.DirFS(*web)
		log.Printf("serving client from disk: %s", *web)
	} else {
		sub, err := fs.Sub(lanesiege.Web, "web")
		if err != nil {
			log.Fatalf("embedded assets: %v", err)
		}
		webFS = sub
	}

	srv := server.New(webFS)

	// Graceful shutdown on Ctrl+C / SIGTERM (docker stop, systemd stop, …).
	go func() {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		<-ctx.Done()
		log.Printf("shutting down")
		srv.Close()
	}()

	log.Printf("Lane Siege listening on http://0.0.0.0%s", listen)
	log.Printf("Open two browser tabs (or two devices) and share a room code.")
	if err := srv.ListenAndServe(listen); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

