// Command icon writes the .iconset macOS wants for an application icon.
//
// The image is drawn from the same geometry as the status bar icon rather than
// checked in as files, so the repository keeps no binary blobs and every size
// is rendered rather than resampled.
//
//	go run ./build/icon dist/awsm.iconset
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"awsm-desktop/internal/trayicon"
)

// The ten images iconutil expects, by the names it expects.
var sizes = []struct {
	name string
	px   int
}{
	{"icon_16x16.png", 16},
	{"icon_16x16@2x.png", 32},
	{"icon_32x32.png", 32},
	{"icon_32x32@2x.png", 64},
	{"icon_128x128.png", 128},
	{"icon_128x128@2x.png", 256},
	{"icon_256x256.png", 256},
	{"icon_256x256@2x.png", 512},
	{"icon_512x512.png", 512},
	{"icon_512x512@2x.png", 1024},
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: icon <output.iconset>")
		os.Exit(2)
	}
	dir := os.Args[1]

	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Each size is drawn at its own resolution. Two of them share a pixel size
	// and are rendered twice, which costs nothing and keeps the table readable.
	for _, size := range sizes {
		path := filepath.Join(dir, size.name)
		if err := os.WriteFile(path, trayicon.AppIcon(size.px), 0644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Printf("wrote %d images to %s\n", len(sizes), dir)
}
