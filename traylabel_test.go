package main

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"awsm-desktop/internal/panel"
)

func TestTheDotIsColouredAndTheWordsAreNot(t *testing.T) {
	// The point of the whole arrangement. A part carrying no colour inherits
	// the menu bar's own, so the words follow the appearance of whichever
	// display they are drawn on -- while the circle stays green.
	parts, err := parseTrayLabel(escape(dotGreen) + dotGlyph + reset + " 9m59s")
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 {
		t.Fatalf("got %d parts, want the dot and the words separately: %+v", len(parts), parts)
	}

	if parts[0].Text != dotGlyph {
		t.Errorf("the first part is %q, want the circle", parts[0].Text)
	}
	if parts[0].FgColor != dotGreen {
		t.Errorf("the circle is %q, want %q", parts[0].FgColor, dotGreen)
	}
	if parts[1].Text != " 9m59s" {
		t.Errorf("the second part is %q, want the words", parts[1].Text)
	}
	if parts[1].FgColor != "" {
		t.Errorf("the words are painted %q; they should follow the menu bar", parts[1].FgColor)
	}
}

func TestAnEscapeSequenceSurvivesTheRoundTrip(t *testing.T) {
	// escape writes it and parseTrayLabel reads it. Nothing else does either,
	// so the two only have to agree with each other -- which is exactly the
	// kind of agreement that quietly stops holding.
	for _, colour := range []string{dotGreen, dotAmber, "#000000", "#ffffff"} {
		parts, err := parseTrayLabel(escape(colour) + "x")
		if err != nil {
			t.Fatal(err)
		}
		if len(parts) != 1 || parts[0].FgColor != colour {
			t.Errorf("%s came back as %+v", colour, parts)
		}
	}
}

func TestAnUnknownSequenceLeavesTheColourAlone(t *testing.T) {
	// The parser only claims to understand what this program writes. Anything
	// else must not silently repaint the label.
	parts, err := parseTrayLabel(escape(dotGreen) + dotGlyph + "\033[1m" + "bold?")
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range parts {
		if part.FgColor != dotGreen {
			t.Errorf("part %q lost its colour to an unknown sequence", part.Text)
		}
	}
}

func TestAPlainLabelNeedsNoColourAtAll(t *testing.T) {
	// No profile, nothing to say. The framework only calls the parser when the
	// label has escape codes in it, so this is really about not writing any.
	if got := trayLabel(panel.StatusOnly{}, ""); got != "" {
		t.Errorf("got %q, want nothing", got)
	}
}

func TestTheLabelTheFrameworkSeesStillReadsAsTheLabel(t *testing.T) {
	// Whatever the escaping does, the text arriving in the menu bar has to be
	// the text label() meant to put there.
	status := panel.StatusOnly{Profile: "work", TTL: "2m", Blocked: "MFA"}

	parts, err := parseTrayLabel(trayLabel(status, ""))
	if err != nil {
		t.Fatal(err)
	}
	var text string
	for _, part := range parts {
		text += part.Text
	}
	if want := label(status, ""); text != want {
		t.Errorf("the menu bar would show %q, want %q", text, want)
	}
}

func TestTheDotArrivesColouredAtTheMenuBar(t *testing.T) {
	// End to end through the function the status bar actually calls. The tests
	// above build the escaped string by hand, which left nothing checking that
	// trayLabel wraps the dot at all -- removing the wrapping entirely kept
	// them all green.
	if runtime.GOOS != "darwin" {
		t.Skip("only macOS renders an attributed label; elsewhere the label goes out plain")
	}

	for name, c := range map[string]struct {
		status panel.StatusOnly
		want   string
	}{
		"healthy":        {panel.StatusOnly{Profile: "work", TTL: "47m41s"}, dotGreen},
		"nearly expired": {panel.StatusOnly{Profile: "work", TTL: "3m"}, dotAmber},
		"waiting on a code": {
			panel.StatusOnly{Profile: "work", TTL: "47m41s", Blocked: "MFA"}, dotAmber,
		},
	} {
		t.Run(name, func(t *testing.T) {
			parts, err := parseTrayLabel(trayLabel(c.status, ""))
			if err != nil {
				t.Fatal(err)
			}
			if len(parts) == 0 {
				t.Fatal("the label has no parts at all")
			}
			if parts[0].Text != dotGlyph {
				t.Fatalf("the label starts with %q, want the dot", parts[0].Text)
			}
			if parts[0].FgColor != c.want {
				t.Errorf("the dot is %q, want %q", parts[0].FgColor, c.want)
			}
		})
	}
}

func TestTheParserIsActuallyInstalled(t *testing.T) {
	// The one line none of the tests above can reach, because it runs inside
	// main. Without it the framework's own parser strips the escape codes and
	// the dot comes out in the menu bar's own colour -- which looks like a
	// design decision rather than a bug, and so would not get reported.
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "application.SystemTrayLabelParser = parseTrayLabel") {
		t.Error("main never installs parseTrayLabel: the dot will not be coloured")
	}
}
