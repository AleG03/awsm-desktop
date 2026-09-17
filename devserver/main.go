//go:build devserver

// Command devserver runs the panel's handler over plain HTTP, so the interface
// can be opened in an ordinary browser with developer tools instead of only
// inside the status bar window.
//
// Assets are read from disk rather than embedded, so editing the page and
// reloading is enough to see the change.
//
//	go run -tags devserver ./devserver
package main

import (
	"log"
	"log/slog"
	"net/http"
	"os"

	"awsm-desktop/internal/awsm"
	"awsm-desktop/internal/panel"
)

const address = "127.0.0.1:8777"

func main() {
	client, err := awsm.New()
	if err != nil {
		log.Fatal(err)
	}

	server := panel.New(
		client,
		os.DirFS("assets"),
		slog.New(slog.NewTextHandler(os.Stderr, nil)),
		func() { os.Exit(0) },
	)

	log.Printf("panel on http://%s (assets read from ./assets)", address)
	log.Fatal(http.ListenAndServe(address, server.Handler()))
}
