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

	// The update check, with the version under the operator's control so both
	// answers can be looked at: AWSM_DESKTOP_VERSION=0.0.1 to see one offered,
	// unset to see what a build made by hand reports. Nothing is opened here --
	// the address is printed instead, which is what there is to check.
	server.OnUpdates(os.Getenv("AWSM_DESKTOP_VERSION"), func(url string) error {
		log.Printf("would open %s", url)
		return nil
	})

	log.Printf("panel on http://%s (assets read from ./assets)", address)
	log.Fatal(http.ListenAndServe(address, server.Handler()))
}
