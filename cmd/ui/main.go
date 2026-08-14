// Command ui serves the NetCore product preview.
//
// It intentionally uses representative data and no database connection so
// teams can review the full interface before each business API is connected.
package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
)

//go:embed assets
var assets embed.FS

func main() {
	addr := flag.String("addr", "127.0.0.1:3000", "HTTP listen address")
	flag.Parse()

	handler, err := newHandler()
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Addr: *addr, Handler: handler}
	log.Printf("NetCore UI preview available at http://%s", *addr)
	log.Fatal(server.ListenAndServe())
}

func newHandler() (http.Handler, error) {
	content, err := fs.Sub(assets, "assets")
	if err != nil {
		return nil, fmt.Errorf("load UI assets: %w", err)
	}
	files := http.FileServer(http.FS(content))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/portal.html" {
			// A captive-portal handoff may briefly appear in the subsequent
			// RouterOS login URL. The portal page itself must not be cached or
			// forwarded as a Referer while the integration is being reviewed.
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'; connect-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:")
		}
		files.ServeHTTP(w, r)
	}), nil
}
