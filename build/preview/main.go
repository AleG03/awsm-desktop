package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"

	"awsm-desktop/internal/trayicon"
)

func decode(data []byte) image.Image {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		panic(err)
	}
	return img
}

// nearest scales by an integer factor without smoothing, so the pixels stay
// honest about what is actually drawn.
func nearest(src image.Image, factor int) image.Image {
	b := src.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx()*factor, b.Dy()*factor))
	for y := 0; y < b.Dy()*factor; y++ {
		for x := 0; x < b.Dx()*factor; x++ {
			out.Set(x, y, src.At(b.Min.X+x/factor, b.Min.Y+y/factor))
		}
	}
	return out
}

func main() {
	// The template mark first -- the only one macOS uses, shown here in the
	// colour the system would pick for that menu bar -- then the coloured
	// variants the other platforms get.
	states := []trayicon.Status{trayicon.Idle, trayicon.Active, trayicon.Attention}
	bars := []struct {
		dark bool
		bg   color.NRGBA
	}{
		{false, color.NRGBA{0xf2, 0xf2, 0xf2, 0xff}}, // a light menu bar
		{true, color.NRGBA{0x2a, 0x2a, 0x2c, 0xff}},  // a dark one
	}

	const pad, gap = 16, 28
	cell := 44 // the icon's own pixels, drawn at 1:1
	big := 44 * 4

	width := pad*2 + 3*cell + 2*gap + 40 + 3*big + 2*gap
	height := pad*2 + 2*(big+gap)

	canvas := image.NewNRGBA(image.Rect(0, 0, width, height))

	for row, bar := range bars {
		top := pad + row*(big+gap)
		draw.Draw(canvas, image.Rect(0, top-pad/2, width, top+big+pad/2),
			&image.Uniform{bar.bg}, image.Point{}, draw.Src)

		for i, state := range states {
			icon := decode(trayicon.Bar(state, bar.dark))

			// Real size, vertically centred in the band.
			x := pad + i*(cell+gap)
			draw.Draw(canvas, image.Rect(x, top+(big-cell)/2, x+cell, top+(big-cell)/2+cell),
				icon, image.Point{}, draw.Over)

			// And magnified, to see what the antialiasing is doing.
			bx := pad + 3*cell + 2*gap + 40 + i*(big+gap)
			draw.Draw(canvas, image.Rect(bx, top, bx+big, top+big),
				nearest(icon, 4), image.Point{}, draw.Over)
		}
	}

	var out bytes.Buffer
	_ = png.Encode(&out, canvas)
	_ = os.WriteFile(os.Args[1], out.Bytes(), 0644)
}
