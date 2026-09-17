package main

import (
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"awsm-desktop/internal/panel"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// The status bar label is an attributed string, and this is how colour gets
// into it.
//
// The framework hands a label to the system as plain text unless it contains
// ANSI escape codes, in which case it asks a parser to split it into coloured
// parts and builds an attributed title from them. The parser is a package
// variable meant to be replaced, so this file replaces it with one that
// understands exactly what this program writes and nothing else.
//
// Why bother: the dot has to keep its colour while the mark beside it is
// recoloured by the system for whichever display's menu bar it is in. A part
// with no colour of its own inherits the menu bar's, so the words stay adaptive
// and only the circle is painted.

// escape and reset are the sequences written around the dot.
const reset = "\033[0m"

func escape(hex string) string {
	r, g, b := hexBytes(hex)
	return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
}

func hexBytes(hex string) (r, g, b uint8) {
	value, err := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	if err != nil {
		return 0, 0, 0
	}
	return uint8(value >> 16), uint8(value >> 8), uint8(value)
}

// trayLabel is what goes to the status bar.
//
// Only macOS renders an attributed label. Elsewhere the escape codes would be
// shown as the codes themselves, and the dot is drawn into the icon there
// anyway, so the label goes out plain.
func trayLabel(status panel.StatusOnly, doing string) string {
	text := label(status, doing)
	colour := dot(status)

	if runtime.GOOS != "darwin" || colour == "" || !strings.HasPrefix(text, dotGlyph) {
		return text
	}
	return escape(colour) + dotGlyph + reset + text[len(dotGlyph):]
}

// sgr matches a colour escape sequence.
var sgr = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

// parseTrayLabel splits a label into the parts the framework colours.
//
// Deliberately narrow: it understands the true colour foreground sequence and
// the reset, which is all this program writes. Anything else is left as text,
// because a status bar that renders an escape code as an escape code is still
// better than one that renders nothing.
func parseTrayLabel(label string) ([]application.SystemTrayLabelPart, error) {
	var parts []application.SystemTrayLabelPart
	colour := ""

	add := func(text string) {
		if text != "" {
			parts = append(parts, application.SystemTrayLabelPart{Text: text, FgColor: colour})
		}
	}

	end := 0
	for _, match := range sgr.FindAllStringSubmatchIndex(label, -1) {
		add(label[end:match[0]])
		colour = foregroundFrom(label[match[2]:match[3]], colour)
		end = match[1]
	}
	add(label[end:])

	if len(parts) == 0 {
		// The framework falls back to the plain label on an empty result, which
		// for an empty label is the right answer anyway.
		return nil, nil
	}
	return parts, nil
}

// foregroundFrom reads a colour out of an escape sequence's parameters,
// keeping the current one when the sequence says something else.
func foregroundFrom(parameters, current string) string {
	if parameters == "" || parameters == "0" {
		return ""
	}
	fields := strings.Split(parameters, ";")
	if len(fields) != 5 || fields[0] != "38" || fields[1] != "2" {
		return current
	}

	var component [3]uint8
	for i, field := range fields[2:] {
		value, err := strconv.Atoi(field)
		if err != nil || value < 0 || value > 255 {
			return current
		}
		component[i] = uint8(value)
	}
	return fmt.Sprintf("#%02x%02x%02x", component[0], component[1], component[2])
}
