// Package trayicon draws the status bar icon.
//
// The mark is the one awsm already uses: three isometric cubes stacked in a
// corner. It is drawn from geometry rather than shipped as a file, so it stays
// crisp at any scale, the repository keeps no binary blobs, and the shape is
// readable in a diff.
//
// There are two of them, because macOS and everywhere else want opposite
// things.
//
// Mark is a template image: alpha only, recoloured by the system for whichever
// display's menu bar it is sitting in. That is per display and simultaneous --
// dark over one wallpaper, light over another, at the same moment -- and it is
// something only a template can do. A template has no colour to give, so on
// macOS the state is carried by a coloured emoji in the label beside it.
//
// Bar is the coloured version, for the platforms whose tray has no text to put
// a dot in. There the picture has to carry the state, and there is no
// per-display recolouring to lose.
package trayicon

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

const (
	// size is the icon's side in points; macOS status bar icons are 22. The
	// bitmap is drawn at twice that, for Retina displays.
	size  = 22
	scale = 2
	side  = size * scale

	// supersample is how many samples per axis each pixel is averaged over.
	// The mark is nothing but diagonals, and diagonals without this look like
	// staircases at this size.
	supersample = 4
)

// Status is what the status bar icon has to say about the session.
type Status int

const (
	// Idle is no active profile. The icon says nothing, which is this
	// application's rule everywhere else.
	Idle Status = iota
	// Active is a profile set and healthy.
	Active
	// Attention is a profile that needs something only a person can give, or
	// one whose credentials are nearly gone.
	Attention
)

// Mark is the plain mark, as a template image.
//
// No dot and no colour: on macOS the dot lives in the label, because an icon
// that carried it would have to give up being a template -- and then it would
// stay the colour it was drawn while every other icon in the menu bar turned
// white on the display with the dark wallpaper.
func Mark() []byte { return mark }

var mark = encode(paintTemplate())

func paintTemplate() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			if covered := coverage(x, y, side, inMark); covered > 0 {
				img.SetNRGBA(x, y, color.NRGBA{A: uint8(covered*255 + 0.5)})
			}
		}
	}
	return img
}

// Bar draws the coloured status bar icon, for the platforms whose tray shows
// no text.
//
// dark is whether the menu bar is dark, which decides the mark's colour.
func Bar(status Status, dark bool) []byte {
	return encode(paint(status, dark))
}

func paint(status Status, dark bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, side, side))

	mark := markColour(dark)
	dot, hasDot := dotColour(status, dark)

	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			// The mark, moved out of the dot's way when there is one.
			marked := coverage(x, y, side, func(u, v float64) bool {
				if hasDot {
					u, v = makeRoom(u, v)
				}
				return inMark(u, v)
			})
			dotted := 0.0
			if hasDot {
				dotted = coverage(x, y, side, func(u, v float64) bool {
					return within(u, v, dotX, dotY, dotRadius)
				})
			}

			total := marked + dotted
			if total == 0 {
				continue
			}
			// The two never overlap -- the dot sits inside the clearance the
			// mark was denied -- so this is a plain weighted average.
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8((float64(mark.R)*marked + float64(dot.R)*dotted) / total),
				G: uint8((float64(mark.G)*marked + float64(dot.G)*dotted) / total),
				B: uint8((float64(mark.B)*marked + float64(dot.B)*dotted) / total),
				A: uint8(total*255 + 0.5),
			})
		}
	}
	return img
}

// makeRoom maps a point into the smaller mark drawn alongside the dot.
//
// Scaled about the centre and nudged up and to the left, which is enough to
// clear the corner the dot occupies while keeping the three cubes whole.
func makeRoom(u, v float64) (float64, float64) {
	return (u+badgedShift-0.5)/badgedScale + 0.5, (v+badgedShift-0.5)/badgedScale + 0.5
}

// markColour is what the menu bar draws its own text in.
func markColour(dark bool) color.NRGBA {
	if dark {
		return color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	}
	return color.NRGBA{A: 0xff}
}

// The dot's colours, in the accessible variants of the system palette: it is
// seven points across, and the ordinary ones are pitched for larger areas.
var (
	greenOnLight = color.NRGBA{R: 0x1a, G: 0x8c, B: 0x3c, A: 0xff}
	greenOnDark  = color.NRGBA{R: 0x3a, G: 0xdb, B: 0x62, A: 0xff}
	amberOnLight = color.NRGBA{R: 0xb2, G: 0x53, B: 0x00, A: 0xff}
	amberOnDark  = color.NRGBA{R: 0xff, G: 0xb3, B: 0x40, A: 0xff}
)

func dotColour(status Status, dark bool) (color.NRGBA, bool) {
	switch status {
	case Active:
		if dark {
			return greenOnDark, true
		}
		return greenOnLight, true
	case Attention:
		if dark {
			return amberOnDark, true
		}
		return amberOnLight, true
	default:
		return color.NRGBA{}, false
	}
}

// coverage is how much of one pixel a shape fills, from 0 to 1.
//
// The shape is expressed in a unit square, so the same geometry serves a 22
// point status bar icon and a 1024 pixel application icon. The mark is nothing
// but diagonals, and diagonals without this look like staircases.
func coverage(x, y, px int, inside func(u, v float64) bool) float64 {
	step := 1.0 / float64(supersample)
	var covered int
	for sy := 0; sy < supersample; sy++ {
		for sx := 0; sx < supersample; sx++ {
			// Sampled at the centre of each sub-pixel.
			u := (float64(x) + (float64(sx)+0.5)*step) / float64(px)
			v := (float64(y) + (float64(sy)+0.5)*step) / float64(px)
			if inside(u, v) {
				covered++
			}
		}
	}
	return float64(covered) / float64(supersample*supersample)
}

// The three cubes, in fractions of the icon's side.
//
// Their centres sit where isometric cubes tessellate: two side by side and one
// nested below between them, which is the arrangement of awsm's own icon. The
// horizontal offset is cos(30°) times the radius and the vertical one is three
// quarters of it, which is what makes the faces meet instead of overlapping.
const (
	cube    = 0.215 // circumradius of one cube
	originX = 0.5
	originY = 0.5
	cubeGap = 0.012 // between neighbouring cubes
	faceGap = 0.011 // between the three faces of one cube
	// The dot sits in the bottom right corner.
	dotX      = 0.80
	dotY      = 0.80
	dotRadius = 0.175

	// When the dot is there, the mark steps aside for it rather than having a
	// hole punched through it. Punching was what the warning badge used to do,
	// and it left the bottom right cube visibly chewed -- tolerable for a state
	// you rarely see, not for the one the icon is in all day.
	badgedScale = 0.84
	badgedShift = 0.045
)

var cubes = [3][2]float64{
	{originX - cube*math.Sqrt(3)/2, originY - cube*0.78}, // upper left
	{originX + cube*math.Sqrt(3)/2, originY - cube*0.78}, // upper right
	{originX, originY + cube*0.78},                       // below, between them
}

func inMark(u, v float64) bool {
	for _, c := range cubes {
		if inCube(u, v, c[0], c[1]) {
			return true
		}
	}
	return false
}

// inCube reports whether a point is on one of a cube's three visible faces.
//
// The cube is a hexagon; the faces are the three quadrilaterals that meet at
// its centre. Rather than fill each face separately, the hexagon is filled and
// the seams are cut back out, which keeps the geometry to one polygon.
func inCube(u, v, cx, cy float64) bool {
	r := cube - cubeGap
	if !inHexagon(u-cx, v-cy, r) {
		return false
	}
	// The three seams radiate from the centre: up-left, up-right and straight
	// down, which is where the top, left and right faces meet.
	for _, angle := range []float64{150, 30, 270} {
		radians := angle * math.Pi / 180
		ex, ey := r*math.Cos(radians), -r*math.Sin(radians)
		if distanceToSegment(u-cx, v-cy, 0, 0, ex, ey) < faceGap/2 {
			return false
		}
	}
	return true
}

// inHexagon reports whether a point lies in a pointy-topped regular hexagon
// centred on the origin, which is what an isometric cube's outline is.
func inHexagon(x, y, r float64) bool {
	x, y = math.Abs(x), math.Abs(y)
	if y > r {
		return false
	}
	// The two slanted sides, as a half-plane test. cos(30°) is the hexagon's
	// half width.
	return x*1.0 <= r*math.Sqrt(3)/2 && y <= r-x/math.Sqrt(3)
}

// distanceToSegment returns how far a point is from a line segment.
func distanceToSegment(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	length := dx*dx + dy*dy
	if length == 0 {
		return math.Hypot(px-ax, py-ay)
	}
	t := ((px-ax)*dx + (py-ay)*dy) / length
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(px-(ax+t*dx), py-(ay+t*dy))
}

func within(u, v, cx, cy, r float64) bool {
	return math.Hypot(u-cx, v-cy) <= r
}

// --- the application icon ---------------------------------------------------
//
// The same three cubes, this time the way awsm draws them everywhere else: in
// white on a dark rounded square. Drawn rather than shipped for the same reason
// as the status bar icon -- it stays crisp at every size the bundle asks for,
// and the repository keeps no binary blobs.

const (
	// The rounded square, as a superellipse. macOS rounds an application icon
	// with a continuous curve rather than an arc, which is what the exponent
	// is for: a circular corner next to Apple's own icons looks wrong.
	squircleExponent = 5.0
	squircleInset    = 0.02

	// markScale shrinks the mark inside the ground. Measured off awsm's own
	// icon, where the cubes span about two thirds of the square while this
	// geometry on its own spans three quarters.
	markScale = 0.88
)

// The ground, top to bottom. Taken from awsm's icon rather than invented.
var (
	groundTop    = [3]float64{0x21, 0x26, 0x33}
	groundBottom = [3]float64{0x0f, 0x12, 0x18}
)

// AppIcon draws the application icon at the given size in pixels.
func AppIcon(px int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, px, px))

	for y := 0; y < px; y++ {
		for x := 0; x < px; x++ {
			ground := coverage(x, y, px, inSquircle)
			if ground == 0 {
				continue
			}

			// The mark, scaled about the centre so it sits inside the ground
			// with room to breathe.
			mark := coverage(x, y, px, func(u, v float64) bool {
				return inMark((u-0.5)/markScale+0.5, (v-0.5)/markScale+0.5)
			})

			base := gradient((float64(y) + 0.5) / float64(px))
			img.SetNRGBA(x, y, color.NRGBA{
				R: mix(base[0], 0xff, mark),
				G: mix(base[1], 0xff, mark),
				B: mix(base[2], 0xff, mark),
				A: uint8(ground*255 + 0.5),
			})
		}
	}
	return encode(img)
}

// inSquircle reports whether a point is inside the rounded square.
func inSquircle(u, v float64) bool {
	half := 0.5 - squircleInset
	x, y := math.Abs(u-0.5)/half, math.Abs(v-0.5)/half
	return math.Pow(x, squircleExponent)+math.Pow(y, squircleExponent) <= 1
}

func gradient(v float64) [3]float64 {
	var out [3]float64
	for i := range out {
		out[i] = groundTop[i] + (groundBottom[i]-groundTop[i])*v
	}
	return out
}

// mix blends towards the mark's white by how much of the pixel it covers.
func mix(base, over, amount float64) uint8 {
	return uint8(base + (over-base)*amount + 0.5)
}

func encode(img image.Image) []byte {
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		// Encoding an in-memory image has no failure mode that is not a bug in
		// this file.
		panic(err)
	}
	return buffer.Bytes()
}
