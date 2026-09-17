// Command awsm-desktop puts the active AWS profile in the status bar and opens
// a panel to search profiles, switch between them and reach the AWS console.
//
// It drives the awsm command line tool rather than talking to AWS itself: awsm
// already resolves credentials, refreshes SSO sessions and builds console
// sign-in URLs, and asking it costs about five milliseconds.
package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"awsm-desktop/internal/awsm"
	"awsm-desktop/internal/logs"
	"awsm-desktop/internal/panel"
	"awsm-desktop/internal/settings"
	"awsm-desktop/internal/trayicon"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:assets
var assets embed.FS

// version is this build's own version, set at link time by build/package.sh
// from the tag being released.
//
// It stays empty for a build made any other way -- `make app`, or `go build`
// while working -- and an empty version is reported as a development build
// rather than compared against the releases, which would otherwise call every
// local build out of date on every check.
var version string

const (
	// Wide enough for nine names out of ten on a single line. Measured against
	// the 304 profiles this was built for: 400 points wraps 80 of them, 440
	// wraps 31, and past that the curve flattens while the panel keeps growing.
	// The rest wrap rather than being cut short, because the name is the only
	// thing that tells two profiles apart.
	panelWidth  = 440
	panelHeight = 540

	// sessionChanged is emitted to the page when the active session changes
	// underneath it.
	//
	// The panel has no other way to find out. A switch from the right click
	// menu happens entirely in Go, and the panel stays on screen showing
	// whatever it last drew -- which is the profile you just switched away
	// from. The name is matched in assets/panel.js.
	sessionChanged = "awsm:session-changed"

	// bundleID matches the one in build/package.sh. It is what macOS uses to
	// tell one application from another, and what the single instance lock is
	// named after.
	bundleID = "io.github.aleg03.awsm.desktop"

	// statusInterval is how often the status bar refreshes on its own.
	//
	// The reading is two local file reads through awsm, so this is not a
	// background job that costs anything. It is only the fallback: anything
	// the user does through the panel refreshes the status bar immediately,
	// because a menu bar that lags a click by fifteen seconds looks broken.
	statusInterval = 15 * time.Second
)

func main() {
	// Everything is written twice: to standard error, so running the binary
	// from a terminal behaves like a command line program, and to a file,
	// because started from the Finder or as a login item there is no terminal
	// and standard error goes nowhere at all.
	out, closeLog, logPath := logs.Writer()
	defer closeLog()
	log := slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelInfo}))
	log.Info("starting", "log", logPath)

	// Before anything sets a label: this is what turns the escape codes around
	// the dot into an actual colour rather than into stripped-out noise.
	application.SystemTrayLabelParser = parseTrayLabel

	client, err := awsm.New()
	if err != nil {
		// Without the CLI there is nothing this application can do. It used to
		// exit here, which as a login item meant the application simply never
		// appeared and left nothing to read.
		log.Error("awsm not found", "error", err)
		reportMissing(err)
		return
	}
	log.Info("using awsm", "path", client.Binary())

	panelAssets, err := fs.Sub(assets, "assets")
	if err != nil {
		log.Error("embedded assets are missing", "error", err)
		os.Exit(1)
	}

	// showPanel reveals the panel without hiding it again. It is a variable
	// because the tray that knows how to position the window is built further
	// down, while the options above already need something to call.
	//
	// Not a toggle: someone who launches the application a second time is
	// asking to see it, and toggling would close the panel they just opened.
	showPanel := func() {}

	// activity is what a long operation is doing, or nil when nothing is.
	//
	// Two things read it: the status bar, which says so, and the handler below
	// that would otherwise dismiss the panel at the worst possible moment.
	var activity atomic.Pointer[string]

	var app *application.App
	server := panel.New(client, panelAssets, log, func() { app.Quit() })

	// Wails' own file server, so that /wails/runtime.js resolves. The page
	// needs it: that script is what turns a right click into the native menu.
	server.UseAssetHandler(application.BundledAssetFileServer(panelAssets))

	app = application.New(application.Options{
		Name:        "awsm",
		Description: "AWS profile switcher in the status bar",
		// A second copy would put a second icon in the menu bar with no way to
		// tell which one answers, and two of these polling the same files is
		// nobody's idea of a good time. Launching again reveals the panel,
		// which is what someone clicking the application twice wants anyway.
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: bundleID,
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				showPanel()
			},
		},
		Assets: application.AssetOptions{
			Handler: server.Handler(),
		},
		Mac: application.MacOptions{
			// Accessory keeps the app out of the Dock and the app switcher: it
			// lives in the status bar, and a Dock icon would be noise.
			ActivationPolicy: application.ActivationPolicyAccessory,
		},
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: true,
		},
	})

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:          "panel",
		Width:         panelWidth,
		Height:        panelHeight,
		Frameless:     true,
		AlwaysOnTop:   true,
		Hidden:        true,
		DisableResize: true,
		URL:           "/",
		Mac: application.MacWindow{
			// Translucent is what makes the panel read as part of the system
			// rather than as a web page pinned to the screen.
			Backdrop: application.MacBackdropTranslucent,
			// macOS fixes a window's appearance when it is created and offers
			// no way to change it afterwards, so a chosen theme is honoured
			// from here. The page paints its own background to match, which is
			// what makes the choice take effect without a restart as well.
			Appearance: appearanceFor(settings.Load().Theme),
		},
		Windows: application.WindowsWindow{
			HiddenOnTaskbar: true,
		},
	})

	// A popover that stays open when you click elsewhere feels broken, and the
	// system tray does not do this for us.
	//
	// The exception is the reason this is not a one-liner. Switching onto a
	// lapsed SSO session makes awsm open a browser; the browser takes the
	// focus; this fires; and the panel disappears taking with it the message
	// explaining what just happened and the button to stop it. So while an
	// operation is waiting on the user, the panel stays.
	window.OnWindowEvent(events.Common.WindowLostFocus, func(*application.WindowEvent) {
		if activity.Load() != nil {
			return
		}
		window.Hide()
	})

	// The other two ways out: Escape, and any action that sends the user to a
	// browser or a terminal, which should not leave the panel on top of where
	// they were sent.
	server.OnHide(func() { window.Hide() })

	// Checking for a new version, and opening its page if the person wants it.
	// Nothing is downloaded and nothing is replaced here: this reports.
	server.OnUpdates(version, app.Browser.OpenURL)

	tray := app.SystemTray.New()
	installIcon(tray)

	// AttachWindow is what makes this a popover rather than a floating window:
	// the tray positions it against the icon and wires left click to toggle it,
	// right click to the menu.
	tray.AttachWindow(window).WindowOffset(5)

	tray.SetMenu(buildMenu(app))
	buildRowMenu(app, server, window, log)
	buildActiveMenu(app, server, window, log)

	// The shortcut is whatever the settings say, re-registered whenever they
	// change. Nothing is claimed by default: taking a key combination from
	// every other application on the machine is not a sensible thing to do
	// without being asked.
	showPanel = func() {
		if window.IsVisible() {
			window.Focus()
			return
		}
		tray.ToggleWindow()
	}

	// The shortcut does toggle, because pressing it again is how you put the
	// panel away without reaching for the mouse.
	shortcut := &shortcut{keys: app.GlobalShortcut, open: func() { tray.ToggleWindow() }}
	if err := shortcut.apply(settings.Load()); err != nil {
		log.Warn("could not register the saved shortcut", "error", err)
	}

	server.OnSettings(shortcut.apply)
	server.OnCopy(app.Clipboard.SetText)
	server.OnLoginItem(
		func() bool {
			status, err := app.Autostart.Status()
			if err != nil {
				log.Debug("could not read the login item", "error", err)
				return false
			}
			return status.Enabled
		},
		func(enabled bool) error {
			if enabled {
				return app.Autostart.Enable()
			}
			return app.Autostart.Disable()
		},
	)

	// Buffered by one, and dropped when full: several actions in a row need
	// one refresh afterwards, not a queue of them.
	refresh := make(chan struct{}, 1)
	announce := func() {
		select {
		case refresh <- struct{}{}:
		default:
		}
	}
	server.OnChanged(func() {
		announce()
		app.Event.Emit(sessionChanged)
	})

	// Only the pointer is written here. followStatus is the one goroutine that
	// touches the status bar, and calling SetLabel from an HTTP handler's
	// goroutine is exactly the cross-thread call the menu items above take care
	// to avoid.
	server.OnBusy(func(doing string) {
		if doing == "" {
			activity.Store(nil)
		} else {
			activity.Store(&doing)
		}
		announce()
	})

	// For the platforms whose icon carries the dot and is therefore painted
	// rather than recoloured: when the menu bar changes colour, it has to be
	// drawn again. On macOS this does nothing, and correctly so -- the mark is
	// a template image and the system repaints it without being asked.
	app.Event.OnApplicationEvent(events.Common.ThemeChanged, func(*application.ApplicationEvent) { announce() })

	go followStatus(app, tray, server, log, refresh, &activity)

	if err := app.Run(); err != nil {
		log.Error("the application stopped", "error", err)
		os.Exit(1)
	}
}

// rowMenu is the name the page attaches to each profile row.
// The two native menus, by the names the page attaches to its elements.
const (
	rowMenu    = "profile"
	activeMenu = "active-profile"
)

// menuItems adds an item that acts on the profile the menu was opened on.
//
// Each one runs off the main thread: switching a profile takes the better part
// of a second, and the interface should not freeze while it does.
func menuItem(app *application.App, menu *application.ContextMenu, log *slog.Logger,
	label string, do func(context.Context, string) error,
) {
	menu.Add(label).OnClick(func(ctx *application.Context) {
		name := ctx.ContextMenuData()
		if name == "" {
			return
		}
		go func() {
			if err := do(context.Background(), name); err != nil {
				log.Error("context menu action failed", "item", label, "profile", name, "error", err)
				app.Dialog.Error().SetTitle(label).SetMessage(err.Error()).Show()
			}
		}()
	})
}

// buildRowMenu is the menu that appears on right clicking a profile in the list.
//
// It exists because the alternative did not work. Actions used to sit in a bar
// along the bottom acting on the selected row, and the selection follows the
// pointer -- so reaching the bar dragged the selection across every row in
// between, and the only reliable way to act on a profile was to activate it
// first. A context menu carries the row it was opened on, so it cannot target
// the wrong one.
func buildRowMenu(app *application.App, server *panel.Server, window *application.WebviewWindow, log *slog.Logger) {
	menu := app.ContextMenu.New()
	item := func(label string, do func(context.Context, string) error) {
		menuItem(app, menu, log, label, do)
	}

	item("Switch to This Profile", func(ctx context.Context, name string) error {
		if server.NeedsMFA(ctx, name) {
			// The code is read from standard input, which a panel has none of.
			return server.Terminal(name)
		}
		return server.Switch(name)
	})

	menu.AddSeparator()
	addLookingActions(item, server, window)
	menu.AddSeparator()
	addCopyActions(item, server)

	app.ContextMenu.Add(rowMenu, menu)
}

// buildActiveMenu is the menu on the profile at the top of the panel.
//
// The same actions minus the one that makes no sense -- it is already the
// active profile -- plus the one that only makes sense here: letting go of it.
func buildActiveMenu(app *application.App, server *panel.Server, window *application.WebviewWindow, log *slog.Logger) {
	menu := app.ContextMenu.New()
	item := func(label string, do func(context.Context, string) error) {
		menuItem(app, menu, log, label, do)
	}

	addLookingActions(item, server, window)
	menu.AddSeparator()
	addCopyActions(item, server)
	menu.AddSeparator()

	// Clear takes no profile: awsm clears whatever is set. The name is carried
	// anyway so that a menu opened on a stale header cannot act on something
	// the user is no longer looking at.
	item("Clear This Profile", func(ctx context.Context, name string) error {
		return server.ClearIfActive(ctx, name)
	})

	app.ContextMenu.Add(activeMenu, menu)
}

type addItem func(label string, do func(context.Context, string) error)

// addLookingActions are the ones that open an account without working in it.
//
// Not activating the profile is the point: looking at an account is not the
// same as starting to work in it.
func addLookingActions(item addItem, server *panel.Server, window *application.WebviewWindow) {
	item("Open Console", func(_ context.Context, name string) error {
		window.Hide()
		return server.Console(name, "")
	})
	item("Open in Firefox Container", func(_ context.Context, name string) error {
		window.Hide()
		return server.Console(name, "firefox")
	})
}

func addCopyActions(item addItem, server *panel.Server) {
	item("Copy Account ID", func(ctx context.Context, name string) error {
		account, err := server.AccountFor(ctx, name)
		if err != nil {
			return err
		}
		return server.Copy(account)
	})
	item("Copy Profile Name", func(_ context.Context, name string) error {
		return server.Copy(name)
	})
}

// reportMissing says, on screen, that the CLI this application drives is not
// there.
//
// It needs a running application to show a dialog, so the whole thing is built
// for one message and torn down again. The alternative -- what this used to do
// -- was a line on standard error that nobody launching from the Finder would
// ever see.
func reportMissing(err error) {
	app := application.New(application.Options{
		Name: "awsm",
		Mac:  application.MacOptions{ActivationPolicy: application.ActivationPolicyAccessory},
	})

	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		dialog := app.Dialog.Error().
			SetTitle("awsm was not found").
			SetMessage(fmt.Sprintf(
				"%v\n\nawsm-desktop is a front end for the awsm command line tool "+
					"and cannot do anything without it.\n\nIt looks in:\n  %s\n\n"+
					"then on PATH. Set AWSM_BIN to point somewhere else.",
				err, strings.Join(awsm.SearchPaths(), "\n  ")))

		// The button is explicit because Show() returns as soon as the dialog
		// is queued rather than when it is dismissed, so quitting after it
		// would take the dialog off the screen before it could be read.
		dialog.AddButton("Quit").SetAsDefault().OnClick(app.Quit)
		dialog.Show()
	})

	_ = app.Run()
}

// appearanceFor maps the theme preference onto the window's appearance.
//
// Following the system is the default and the empty value, which is exactly
// what AppKit treats as "inherit".
func appearanceFor(theme string) application.MacAppearanceType {
	switch theme {
	case "light":
		return application.NSAppearanceNameAqua
	case "dark":
		return application.NSAppearanceNameDarkAqua
	default:
		return application.DefaultAppearance
	}
}

// buildMenu is the right click menu.
//
// It holds one thing. Preferences used to live here and now live in the panel,
// where there is room to explain them and where the user is already looking.
func buildMenu(app *application.App) *application.Menu {
	menu := app.NewMenu()
	menu.Add("Quit awsm").OnClick(func(*application.Context) { app.Quit() })
	return menu
}

// shortcut owns the one system wide accelerator this application registers.
//
// It remembers what is currently registered because changing the preference
// means releasing the old combination before claiming the new one -- and if the
// new one is refused, putting the old one back. Otherwise asking for a
// combination another application already owns costs you the one you had.
type shortcut struct {
	// An interface rather than the framework's manager, so that the recovery
	// below can be tested. What it does when a combination is refused is the
	// whole of its behaviour, and it is not something to find out about by
	// losing a working shortcut.
	keys    accelerators
	open    func()
	current string
}

// accelerators is the part of the framework this needs.
type accelerators interface {
	Register(accelerator string, callback func()) error
	Unregister(accelerator string) error
}

func (s *shortcut) apply(preferences settings.Settings) error {
	wanted := preferences.Shortcut
	if wanted == s.current {
		return nil
	}

	previous := s.current
	if s.current != "" {
		if err := s.keys.Unregister(s.current); err != nil {
			return fmt.Errorf("could not release %s: %w", s.current, err)
		}
		s.current = ""
	}
	if wanted == "" {
		return nil
	}

	if err := s.keys.Register(wanted, s.open); err != nil {
		// Usually means another application already owns the combination. The
		// old one has been released by now, so put it back: the user asked to
		// change their shortcut, not to be left without one. This is what the
		// comment on the type above always claimed and the code did not do.
		if previous != "" {
			if again := s.keys.Register(previous, s.open); again == nil {
				s.current = previous
			}
		}
		return fmt.Errorf("%s is not available: %w", wanted, err)
	}
	s.current = wanted
	return nil
}

// followStatus keeps the status bar showing the active profile.
func followStatus(app *application.App, tray *application.SystemTray, server *panel.Server, log *slog.Logger, refresh <-chan struct{}, activity *atomic.Pointer[string]) {
	ticker := time.NewTicker(statusInterval)
	defer ticker.Stop()

	update := func() {
		doing := ""
		if current := activity.Load(); current != nil {
			doing = *current
		}

		status, err := server.Status(context.Background())
		if err != nil {
			// Transient: the next tick tries again. Saying so every fifteen
			// seconds would bury anything worth reading.
			log.Debug("could not read the status", "error", err)
			return
		}
		tray.SetLabel(trayLabel(status, doing))
		tray.SetTooltip(tooltip(status, doing))
		updateIcon(tray, iconStatus(status))
	}

	update()
	for {
		select {
		case <-ticker.C:
			update()
		case <-refresh:
			update()
		case <-app.Context().Done():
			return
		}
	}
}

// installIcon puts the mark in the status bar, once.
//
// On macOS it is a template image, which is the only kind the system recolours
// for the display it is being drawn on. That matters because the menu bar's
// appearance is per display and simultaneous: light on the built-in screen,
// dark on an external one whose wallpaper is dark. A coloured bitmap stays the
// colour it was drawn and ends up black on a dark menu bar while every other
// icon has turned white -- which is exactly what it did.
//
// So on macOS the mark never changes and the state is the coloured dot in the
// label. Elsewhere the tray has no text to put a dot in, so the picture carries
// it and changes with it.
func installIcon(tray *application.SystemTray) {
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(trayicon.Mark())
		return
	}
	updateIcon(tray, trayicon.Idle)
}

// updateIcon redraws the icon for a state, where the icon is what carries it.
func updateIcon(tray *application.SystemTray, status trayicon.Status) {
	if runtime.GOOS == "darwin" {
		// The dot is in the label, and the mark is already installed. Setting
		// a plain icon here would also turn the template off for good: the
		// framework never clears that flag once set.
		return
	}
	tray.SetIcon(trayicon.Bar(status, false))
	tray.SetDarkModeIcon(trayicon.Bar(status, true))
}

// iconStatus is what the icon should say about a session.
//
// The dot is the answer to "am I working in an account right now", which is the
// question the menu bar is glanced at for. Amber rather than green is the
// answer to "and is it about to stop working".
func iconStatus(status panel.StatusOnly) trayicon.Status {
	if status.Profile == "" {
		return trayicon.Idle
	}
	if needsAttention(status) {
		return trayicon.Attention
	}
	return trayicon.Active
}

// needsAttention reports whether something is worth interrupting for: the
// daemon is stuck on something only a person can do, or the credentials are
// close enough to expiry to matter.
func needsAttention(status panel.StatusOnly) bool {
	return status.Blocked != "" || panel.ExpiringSoon(status.TTL)
}

// dotGlyph is the circle beside the mark.
//
// An ordinary text character, not an emoji. An emoji carries its own colours
// and needs no help -- which is why it was tried first -- but it is drawn from
// a colour font at that font's own proportions, and next to a twenty-two point
// mark it is far too big. A text glyph takes the menu bar's font and its size,
// and gets its colour from the attributed label instead.
const dotGlyph = "●"

// The dot's colours.
//
// One value each rather than one per appearance: the label is a single
// attributed string shown on every display at once, and the menu bar it sits in
// may be light on one screen and dark on another. So these are mid-tones that
// read against both, rather than the light and dark variants of the system
// palette.
const (
	dotGreen = "#1c9e3e"
	dotAmber = "#d97706"
)

// dot reports whether there is a circle to draw, and in what colour.
func dot(status panel.StatusOnly) string {
	switch iconStatus(status) {
	case trayicon.Active:
		return dotGreen
	case trayicon.Attention:
		return dotAmber
	default:
		return ""
	}
}

// label is the text shown next to the icon.
//
// The dot says whether there is a session and whether it is healthy. Words are
// added only when there is something a colour cannot say: a profile name here
// would be forty characters of menu bar and is one click away in the panel.
//
// Only macOS renders this. A Windows tray item has an icon and a tooltip and
// nothing else, and on Linux the StatusNotifierItem title is advisory -- which
// is why the icon carries the dot on those platforms instead.
func label(status panel.StatusOnly, doing string) string {
	parts := []string{}
	if dot(status) != "" {
		parts = append(parts, dotGlyph)
	}

	switch {
	case doing != "":
		// Something is waiting on the user, and the panel may not be on screen
		// to say so: what they are looking at is the browser awsm just opened.
		parts = append(parts, doing)
	case status.Blocked != "":
		// Nothing will renew until a person acts, so say which kind of acting.
		parts = append(parts, status.Blocked)
	case panel.ExpiringSoon(status.TTL):
		parts = append(parts, status.TTL)
	}

	return strings.Join(parts, " ")
}

// tooltip carries the full truth, including the parts the label had to drop.
func tooltip(status panel.StatusOnly, doing string) string {
	if status.Profile == "" {
		if doing != "" {
			return "awsm — " + doing
		}
		return "awsm — no active profile"
	}
	parts := []string{status.Profile}
	if doing != "" {
		// First, because it is the only part of this that is happening now.
		parts = []string{doing, status.Profile}
	}
	if status.Region != "" {
		parts = append(parts, status.Region)
	}
	if status.TTL != "" {
		parts = append(parts, status.TTL)
	}
	if status.Blocked != "" {
		parts = append(parts, fmt.Sprintf("needs %s", status.Blocked))
	}
	return strings.Join(parts, " · ")
}
