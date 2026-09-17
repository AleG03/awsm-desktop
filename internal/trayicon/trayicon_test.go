package trayicon

import (
	"bytes"
	"image"
	"image/png"
	"math"
	"testing"
)

func decode(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not a valid PNG: %v", err)
	}
	return img
}

// states is every icon the status bar can show.
var states = map[string]Status{"idle": Idle, "active": Active, "attention": Attention}

func TestEveryStatusIsARetinaSizedPNG(t *testing.T) {
	for name, status := range states {
		for _, dark := range []bool{false, true} {
			bounds := decode(t, Bar(status, dark)).Bounds()
			if bounds.Dx() != side || bounds.Dy() != side {
				t.Errorf("%s (dark=%v) is %dx%d, want %dx%d", name, dark, bounds.Dx(), bounds.Dy(), side, side)
			}
		}
	}
}

func TestTheDotSaysWhichStateTheSessionIsIn(t *testing.T) {
	// The whole reason this is no longer a template image. A template is
	// recoloured by the system from its alpha alone, so the only signal
	// available was a change of shape; a green dot needs to stay green.
	for _, dark := range []bool{false, true} {
		idle := dominantHue(t, Bar(Idle, dark))
		active := dominantHue(t, Bar(Active, dark))
		attention := dominantHue(t, Bar(Attention, dark))

		if idle.green > 0 || idle.amber > 0 {
			t.Errorf("dark=%v: the idle icon has a dot on it", dark)
		}
		if active.green == 0 {
			t.Errorf("dark=%v: the active icon has no green dot", dark)
		}
		if active.amber > 0 {
			t.Errorf("dark=%v: the active icon has amber on it", dark)
		}
		if attention.amber == 0 {
			t.Errorf("dark=%v: the attention icon has no amber dot", dark)
		}
		if attention.green > 0 {
			t.Errorf("dark=%v: the attention icon still has green on it", dark)
		}
	}
}

type hues struct{ green, amber int }

// dominantHue counts the coloured pixels, which is how the dot is found: the
// mark itself is drawn in black or white and has no hue at all.
func dominantHue(t *testing.T, data []byte) hues {
	t.Helper()
	img := decode(t, data)

	var found hues
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			r, g, b, alpha := img.At(x, y).RGBA()
			if alpha < 0xf000 {
				continue
			}
			switch {
			case g > r+0x2000 && g > b+0x2000:
				found.green++
			case r > g+0x2000 && g > b+0x2000:
				found.amber++
			}
		}
	}
	return found
}

func TestTheMarkIsDrawnInTheMenuBarsOwnColour(t *testing.T) {
	// It is not a template any more, so nothing recolours it for us. Getting
	// this backwards would put a black mark on a black menu bar.
	light, dark := decode(t, Bar(Idle, false)), decode(t, Bar(Idle, true))

	sample := func(img image.Image) uint32 {
		for y := 0; y < side; y++ {
			for x := 0; x < side; x++ {
				if r, _, _, alpha := img.At(x, y).RGBA(); alpha > 0xf000 {
					return r
				}
			}
		}
		t.Fatal("the icon is empty")
		return 0
	}

	if sample(light) > 0x4000 {
		t.Error("the mark is light on a light menu bar")
	}
	if sample(dark) < 0xc000 {
		t.Error("the mark is dark on a dark menu bar")
	}
}

func TestTheDotDoesNotEatTheMark(t *testing.T) {
	// The warning badge used to be punched through the mark, which left the
	// bottom right cube visibly chewed. That was tolerable for a state you
	// rarely see and is not for the one the icon is in all day, so the mark
	// steps aside instead.
	//
	// Area is the thing to measure, not shape: a mark that merely got smaller
	// covers exactly the square of the scale, while one with a bite out of it
	// covers less. Counting the cubes does not work -- at twenty-two points
	// their gaps are narrower than a pixel and antialiasing joins them, so even
	// an untouched mark comes out as two shapes rather than three.
	idle := markArea(t, Bar(Idle, true))
	active := markArea(t, Bar(Active, true))

	want := badgedScale * badgedScale
	if got := active / idle; got < want-0.02 || got > want+0.02 {
		t.Errorf("the badged mark covers %.3f of the plain one, want %.3f: the dot is eating it", got, want)
	}
}

// markArea is how much of the icon the mark covers, in pixels, counting a
// half-transparent pixel as half. The dot is excluded: it is the one thing here
// with a colour, while the mark is drawn in black or white.
func markArea(t *testing.T, data []byte) float64 {
	t.Helper()
	img := decode(t, data)

	var total float64
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			r, g, b, alpha := img.At(x, y).RGBA()
			if r == g && g == b {
				total += float64(alpha) / 0xffff
			}
		}
	}
	return total
}

func TestTheShapeHasSoftEdges(t *testing.T) {
	// Aliasing is the only thing that can make a mark this small look cheap.
	// Partially transparent pixels along the curves are what antialiasing
	// leaves behind; none at all would mean staircases.
	img := decode(t, Bar(Active, true))
	partial := 0
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			_, _, _, alpha := img.At(x, y).RGBA()
			if alpha > 0 && alpha < 0xffff {
				partial++
			}
		}
	}
	if partial < side {
		t.Errorf("only %d partially transparent pixels: the curves are aliased", partial)
	}
}

func TestTheIconLeavesMarginAtTheEdges(t *testing.T) {
	// A shape touching the bitmap edge sits flush against its neighbours in
	// the menu bar, which reads as a rendering fault rather than as a design.
	for name, status := range states {
		img := decode(t, Bar(status, true))
		for i := 0; i < side; i++ {
			for _, p := range [][2]int{{i, 0}, {i, side - 1}, {0, i}, {side - 1, i}} {
				if _, _, _, alpha := img.At(p[0], p[1]).RGBA(); alpha != 0 {
					t.Fatalf("%s reaches the edge at (%d,%d)", name, p[0], p[1])
				}
			}
		}
	}
}

func TestTheApplicationIconIsDrawnAtEverySizeAsked(t *testing.T) {
	// Every size is rendered rather than resampled, which is the whole reason
	// the mark is geometry and not a file.
	for _, px := range []int{16, 32, 128, 512, 1024} {
		bounds := decode(t, AppIcon(px)).Bounds()
		if bounds.Dx() != px || bounds.Dy() != px {
			t.Errorf("AppIcon(%d) is %dx%d", px, bounds.Dx(), bounds.Dy())
		}
	}
}

func TestTheApplicationIconIsARoundedSquareWithTheMarkOnIt(t *testing.T) {
	// Three claims, because each one has its own way of going wrong: a square
	// with no rounding, a rounded square with nothing on it, and a mark drawn
	// in the ground's own colour.
	const px = 512
	img := decode(t, AppIcon(px))

	// Just inside the margin, where a square would be solid and a rounded one
	// is not. The outermost pixel is no use: the inset alone leaves that
	// transparent whatever shape is drawn, which is how the first version of
	// this test passed with the rounding removed.
	corner := int(math.Round(squircleInset*px)) + 2
	if _, _, _, alpha := img.At(corner, corner).RGBA(); alpha > 0x2000 {
		t.Error("the corner is opaque: the icon is a square rather than a rounded one")
	}
	if _, _, _, alpha := img.At(px/2, px/2).RGBA(); alpha < 0xf000 {
		t.Error("the middle is transparent: there is no icon here")
	}

	// The mark is white on a dark ground, so both have to be there.
	var light, dark int
	for y := 0; y < px; y++ {
		for x := 0; x < px; x++ {
			r, _, _, alpha := img.At(x, y).RGBA()
			if alpha < 0xf000 {
				continue
			}
			if r > 0xe000 {
				light++
			} else if r < 0x4000 {
				dark++
			}
		}
	}
	if light == 0 {
		t.Error("nothing is drawn on the ground: the cubes are missing")
	}
	if dark == 0 {
		t.Error("there is no dark ground behind the cubes")
	}
	// A mark that filled the square, or a stray pixel passing for one, would
	// both slip past a simple "is there any white" check.
	if share := float64(light) / float64(light+dark); share < 0.15 || share > 0.5 {
		t.Errorf("the mark covers %.0f%% of the ground, which is not what the awsm icon looks like", share*100)
	}
}

func TestTheTemplateMarkCarriesItsShapeInAlphaAlone(t *testing.T) {
	// A template image is recoloured by the system from its alpha channel and
	// its colours are thrown away. That recolouring is per display -- black on
	// the built-in screen's light menu bar, white on an external monitor's dark
	// one, at the same moment -- and it is the reason this image exists.
	//
	// It also means any state expressed as colour in here would simply vanish,
	// which is why the dot is an emoji in the label instead.
	img := decode(t, Mark())

	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			r, g, b, alpha := img.At(x, y).RGBA()
			if alpha == 0 {
				continue
			}
			if r != 0 || g != 0 || b != 0 {
				t.Fatalf("pixel (%d,%d) carries colour (%d,%d,%d): the system will discard it", x, y, r, g, b)
			}
		}
	}
}

func TestTheTemplateMarkIsTheWholeMarkAndNothingElse(t *testing.T) {
	// No dot cut into it and no room made for one: on macOS the dot is beside
	// the icon rather than in it, so the mark gets the whole square. A mark
	// that had stepped aside for a dot that is not there would just be small.
	plain := markArea(t, Mark())
	coloured := markArea(t, Bar(Idle, true))

	if ratio := plain / coloured; ratio < 0.98 || ratio > 1.02 {
		t.Errorf("the template mark covers %.3f of the unbadged coloured one, want the same", ratio)
	}
}
