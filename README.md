# awsm-desktop

The active AWS profile in the status bar, and a panel to change it.

Click the status bar item and a panel opens against it: the profile currently
set, and a search field over every profile you have. Right click any of them, or
use the keys below, to switch to it, open the AWS console, open it in a Firefox
container, or copy its account id. It replaces a Raycast extension that did the
same job, without needing Raycast.

It is a front end for [awsm](https://github.com/AleG03/awsm) and nothing more.
Credentials, SSO sessions and console sign-in URLs are all awsm's work; this
asks it and draws the answer.

## Requirements

- `awsm` on the machine. `/usr/local/bin` and `/opt/homebrew/bin` are checked
  first, then `PATH`. Set `AWSM_BIN` to point somewhere else.
- The **AWS CLI**, which awsm uses for SSO sign-ins. It does not have to be on
  your shell's `PATH` for this to find it — see below.
- Go 1.25 and the Xcode Command Line Tools to build. **Xcode itself is not
  needed.**

## Building

```sh
make app        # assembles dist/awsm.app
make install    # replaces /Applications/awsm.app
```

Both the status bar mark and the application icon are drawn from geometry at
build time — `go run ./build/icon` writes the `.iconset` and `iconutil` turns it
into the `.icns`. Every size is rendered rather than resampled, and the
repository holds no binary files.

The bundle is signed ad-hoc, which is enough to run it yourself and not enough
to hand to anyone else.

`make icon` draws the mark and every coloured state on a light and a dark menu
bar, at real size and magnified, into `dist/icons.png`. On macOS only the
leftmost is used — the system recolours it — but the coloured ones ship on the
other platforms and are worth looking at.

`make run` serves the same panel over `http://127.0.0.1:8777` instead, reading
the page from `assets/` on disk. It is the quickest way to work on the
interface: edit, reload, and use the browser's developer tools.

## Using it

| | |
|---|---|
| Type | filter, by any part of a name, session, region or account — terms may be in any order |
| ↑ ↓ | move through the results |
| ↵ | switch to the selected profile |
| ⌘↵ | open its console **without switching to it** |
| ⌘⇧↵ or ⌘F | the same, in a Firefox container |
| ⌘C / ⌘⇧C | copy the account id / the profile name |
| ⌘T | open a terminal on that profile |
| ⌘K | clear the active profile |
| Esc | clear the search, then close the panel |

**Right clicking a profile** offers the same actions as a native menu, on the
row under the pointer. **Right clicking the profile at the top** offers them for
that one, plus clearing it — `awsm clear` takes no argument and lets go of
whatever is set, so the panel checks that it is still the profile the menu was
opened on before running it.

Only ↵ on its own makes a profile active. Opening a console for an account is
not the same as starting to work in it, and every other action leaves the
active profile alone.

An earlier version put these actions in a bar along the bottom, acting on the
selected row. It could not work: the selection follows the pointer, so reaching
the bar dragged it across every row in between, and the only reliable way to act
on a profile was to activate it first. A context menu carries the row it was
opened on, so it cannot target the wrong one.

### The details, and staying tied to the profile

Clicking the active profile opens its details: the region it is set to, and
**Identity** — the only thing in this application that goes to AWS rather than
to a local file, which is why it is asked for on request rather than on opening
the panel.

Both describe the active profile, so both are redrawn when it changes. They were
not, and both got it wrong in the same way: after a switch the picker showed the
previous profile's region, disagreeing with the region printed two lines above
it out of the same state, and the identity showed one account's ARN under
another profile's name.

That last one is worth being blunt about. It is not "out of date": it is wrong,
in the one place someone looks to be certain which account they are about to act
in. So the identity is tied to the profile it was asked about — switching while
it is on screen asks again, switching while the details are closed forgets it,
and an answer that arrives after the profile has changed is thrown away.

The region picker keeps its list of regions across all this. That list is the
same whatever profile is active, so it is fetched once; only the selected value
follows the profile.

**Settings** is in the footer: a system-wide shortcut that opens the panel from
anywhere, the theme, which browser the **Console** action opens in, whether
credentials are being renewed in the background, and whether awsm starts at
login. It also shows where awsm, the settings file and the log live.
No shortcut is registered until you choose one — claiming a key combination
across the whole machine without being asked is a good way to break somebody
else's application. If the one you pick is already owned by something, the panel
says so and puts your previous one back rather than leaving you with none, and
the refused combination is not written to the settings file: it would fail to
register on the next launch too, and you would be left with no shortcut while
the settings view showed the refused one as though it worked. Right clicking the status bar item offers **Quit**.

### Theme

Following the system is the default and keeps the panel translucent. Choosing
light or dark gives it a solid background instead, because macOS fixes a
window's appearance when the window is created and offers no way to change it
afterwards — a light panel on a dark desktop would otherwise be dark text on a
dark blur.

### Copying

**Copy ID** goes through the platform clipboard rather than
`navigator.clipboard`. The browser API wants a secure context and a user
gesture it recognises, and the custom scheme the webview serves this panel from
is neither.

## The status bar item stays small

It is an icon and, almost always, nothing else. A profile name here runs past
forty characters, and a wide item is the first thing macOS drops when the menu
bar fills up — so the name lives in the tooltip and in the panel, one click
away.

Text appears beside the icon only when it earns the width:

| | |
|---|---|
| `Switching…` | something is waiting on you right now — see below |
| `MFA` / `SSO` | the refresh daemon is stuck on something only you can do |
| `9m59s`, `expired` | the credentials are inside the last ten minutes |

### The dot

awsm's three cubes, with a coloured dot beside them saying what the session is
doing:

| | |
|---|---|
| green ● | a profile is active and healthy |
| amber ● | a profile is active and something needs you |
| no dot | nothing is set |

**The dot is in the label, not in the icon**, and that is not a shortcut. The
menu bar's appearance is *per display and simultaneous*: light on a built-in
screen, dark on an external monitor whose wallpaper is dark, at the same moment.
Only a **template image** — alpha with the colours thrown away — gets recoloured
by the system to match whichever menu bar it is being drawn in.

So the two requirements pull apart. A template has no colour to give; a coloured
bitmap is one picture and stays the colour it was drawn. An icon that carried
the dot in colour came out black on the dark monitor while every other icon in
that menu bar had turned white.

The label resolves it. macOS renders it as an **attributed string**, so each run
can have its own colour — and a run with no colour inherits the menu bar's. The
dot is a plain `●` painted green or amber; the words beside it are left
unpainted and follow the appearance of whichever display they are on.

An emoji was the first attempt and is the wrong tool: it carries its own colours
and needs no help, but it is drawn from a colour font at that font's
proportions, and beside a twenty-two point mark it is enormous. A text glyph
takes the menu bar's own font and size.

Getting colour into the label at all takes one line in `main`: the framework
hands the label over as plain text unless it contains ANSI escape codes, in
which case it asks `application.SystemTrayLabelParser` to split it into coloured
parts. That variable exists to be replaced, and [traylabel.go](traylabel.go)
replaces it with a parser that understands exactly the one sequence this program
writes.

The tray on Windows and Linux shows no text at all, so there the dot goes back
into the picture — `trayicon.Bar` draws the coloured version, and nothing is
lost because there is no per-display recolouring to give up. The label goes out
plain on those platforms; escape codes with nothing to interpret them would be
shown as the codes themselves.

The two colours are mid-tones rather than the light and dark variants of the
system palette, for the same reason the mark is a template: one attributed
string is shown on every display at once, so the dot has to read against a light
menu bar and a dark one simultaneously.

Colour is not the only signal for the amber state. Whenever the dot is amber the
label also carries `MFA`, `SSO` or the countdown — the two are tested against
each other — so nothing is left to hue alone.

### Switching can take a while, and says so

Switching to a profile whose SSO session has lapsed makes awsm open a browser
and wait for the sign-in. That is the right thing for it to do and it can take
as long as a person takes. Three things follow from that, and all three were
wrong at some point:

**The panel stays on screen.** It normally hides the moment it loses focus,
which is what a popover should do — except that the browser awsm just opened
takes the focus, so the panel vanished exactly when it had something to say.
While an action is waiting on you, the hide is suspended.

**The menu bar says so too.** You may well be looking at the browser rather than
at the panel, so `Switching…` or `Signing in…` appears beside the icon until it
is over. This is the one message that outranks the rule about staying quiet.

**Nothing else can interrupt it.** Only the operations that write credentials —
switching, signing in — are mutually exclusive, and a new one replaces the old
because two writers on `~/.aws/credentials` would be a bug nobody could
reproduce. Opening a console writes nothing, so it runs alongside; it used to be
in the same queue, which meant looking at another account while a sign-in was in
progress killed the sign-in, and the panel reported it "Cancelled" to somebody
who had cancelled nothing.

The panel also stops listening to the keyboard while an action runs. Dimming it
only stops the mouse — `pointer-events` says nothing about keys — so pressing
return again because it looked stuck started a second switch, which cancelled
the first. Escape still works: getting the panel out of the way is not an action
on a profile.

**You can stop it.** These commands are bounded at ten minutes — long enough
that a sign-in is never killed underneath you, which the previous bound of
ninety seconds routinely did, and short enough that a wedged process cannot live
forever. Ten minutes is not a thing to sit through, so the panel offers
**Cancel**, and a cancelled action says "Cancelled" rather than showing you the
`signal: killed` it really got.

Everything that only reads local files keeps a much tighter bound: the status
bar asks on a fifteen second timer and has to give up long before the next one.

### The PATH an application is given

A GUI application launched from the Finder or at login gets launchd's `PATH` —
`/usr/bin:/bin:/usr/sbin:/sbin`, and nothing a shell profile would have added.
Finding awsm itself is handled by looking in the usual places by absolute path.

That was enough right up until awsm needed to run something of its own: `awsm
sso login` shells out to the AWS CLI, which installs to `/usr/local/bin` and is
not on that `PATH`. Renewing an SSO session then failed, reporting that `aws`
could not be found — from an application that had launched perfectly well.

So the `PATH` handed to awsm gets `/usr/local/bin`, `/opt/homebrew/bin`,
`/opt/local/bin` and awsm's own directory appended, and only where those
directories exist. Appended rather than prepended: a `PATH` that already names a
tool is one somebody arranged deliberately.

## What it will not do

**Type your MFA code.** awsm reads the code from standard input, and a panel has
none to offer. Profiles that need one are marked `MFA`, and choosing one opens
your default terminal with the command already written. Everything else — SSO
included — happens without leaving the panel, because `awsm sso login` opens the
browser by itself.

**Renew credentials on a schedule.** That is awsm's own refresh daemon
(`awsm daemon enable`). Two writers on `~/.aws/credentials` would be a bug
nobody could reproduce, so this panel only reads what the daemon decided and
shows it: the badged icon and a `⚠ MFA` or `⚠ SSO` in the menu bar, and a banner
in the panel with the button that fixes it.

It does check whether the daemon is running at all, because everything in the
paragraph above is inert when it is not — and a machine where it was never
enabled shows no warnings and looks perfectly healthy. **Settings** always says
which of *Running*, *Not running* or *Unknown* applies, and offers to turn it
on. *Unknown* is a real answer: the state is read out of output meant for a
person, and claiming renewal is off when that cannot be determined would send
you to fix something that is not broken.

The main panel mentions it only when it is actually costing you something: the
daemon is off **and** the credentials on screen are inside the last ten minutes.
Anything more would be a panel nagging about a setting.

## One copy at a time

Launching awsm again while it is already running opens the panel instead of
starting a second copy. Two status bar icons with no way to tell which one
answers is a confusing thing to do to somebody, and two of these reading the
same files is worse.

## When something goes wrong

Everything goes to `~/Library/Logs/awsm/desktop.log` as well as to standard
error, because started from the Finder or as a login item there is no terminal
and standard error goes nowhere. The file is started over once it passes a
megabyte.

If awsm itself cannot be found, the application says so in a dialog and names
the paths it looked in. It used to write that to standard error and exit, which
as a login item meant it simply never appeared.

## Platforms

macOS is what this is built for, and the only platform it has been run on.
There is code for the others and there are reasons to think it works, which is
not the same thing — nobody has started this on a Windows machine or a Linux
one, including the people who wrote the code for them:

- **Windows** — compiles, and is checked on every release build, but has never
  been run. Wails is pure Go there, so the binary is real rather than a stub;
  it would need the WebView2 runtime, which Windows 11 ships and Windows 10
  usually has through Edge. The tray shows no text at all, only an icon and a
  tooltip, so the dot is drawn into the icon rather than sitting beside it, and
  the details go in the tooltip.
- **Linux** — not even compiled here: it needs GTK and WebKit headers. The tray
  would be a StatusNotifierItem over DBus, which is native on KDE Plasma; GNOME
  needs a shell extension, and under Wayland a window cannot place itself, so
  the panel would appear wherever the compositor decides rather than under the
  icon.

## Layout

```
main.go               status bar item, panel window, login item
internal/awsm/        every invocation of the awsm CLI, in one place
internal/panel/       the JSON API the page talks to
internal/logs/        where this application writes what it has to say
internal/settings/    the preferences file
internal/terminal/    handing a command to the user's terminal
internal/trayicon/    the status bar icon, drawn rather than shipped
assets/               the page: one HTML file, one stylesheet, one script
devserver/            the panel over plain HTTP, for development
build/                packaging, and the icons drawn at build time
```

There is no npm, no bundler and no generated bindings. The page asks Go over
`fetch`, because the asset server is an `http.Handler` already and a few JSON
endpoints through it cost nothing.

## Continuous integration

[`.github/workflows/build.yml`](.github/workflows/build.yml) runs **only on a
version tag**, and on a manual dispatch. It formats, vets and tests the code,
then builds the bundle. Nothing watches ordinary commits, so `make test` before
pushing is what catches a mistake before a tag does.

macOS is the only job: the bundle needs `codesign` and `iconutil`, and Wails
links against the system frameworks, so there is nowhere else to build it.
Windows is covered by a cross compile, which needs neither and still catches a
platform-specific file that stopped compiling. Linux is not covered — it needs a
runner with the GTK and WebKit headers, and a job nobody has run is not a check.

### Getting the built app

Pushing a version tag builds the bundle and makes a **release** of it:

```sh
git tag v0.2.0
git push origin v0.2.0
```

One download, `awsm-macos.zip`, and it runs on every Mac: the bundle is
**universal**, carrying both Apple silicon and Intel.

That costs about four megabytes — 8.2 MB against 3.9 for a single architecture,
since the two halves share nothing and each carries the page's assets. The
alternative was two downloads, smaller but asking the person to choose, and
choosing wrong does not fail cleanly: macOS runs the Intel one under Rosetta and
warns about it, which reads as something being wrong with the application rather
than with the download.

Both halves are compiled on the same Apple silicon runner and fused with `lipo`
— the macOS SDK carries both architectures, so the Intel half needs no Intel
runner. That also means the second half could quietly come out a copy of the
first, so the workflow reads back what it built with `lipo -archs` and fails
unless both are in there.

It checks `Info.plist` as well, which is not belt and braces. The plist is
written by a heredoc, so a stray line in the packaging script lands inside it —
and `codesign` does not object: it falls back to an identifier derived from a
hash and signs anyway. A bundle with a corrupt plist will otherwise reach a
release looking perfectly signed.

The version comes from the tag: `VERSION` reaches `build/package.sh`, so what
`CFBundleShortVersionString` says and what the release says cannot disagree.

Every run also keeps the zip as a **workflow artifact**, which is what a manual
dispatch from the Actions tab leaves behind when there is no tag to release:
attached to the run, ninety days, and a GitHub login needed to fetch it.

There is nothing under **Packages**, and there never will be: that is for npm,
Docker, Maven and NuGet registries, and an application bundle is none of them.

`ARCH` picks what to build — `arm64`, `amd64` or `universal` — and defaults to
the machine you are on, so `make app` and `make install` stay local builds and
do not pay for an architecture nobody here is going to run.

```sh
ARCH=universal ./build/package.sh
```

**A released bundle is still signed ad-hoc.** That is enough to run it yourself
and not enough for Gatekeeper on somebody else's machine, which will refuse it
on first launch. Right click the app and choose **Open**, or

```sh
xattr -d com.apple.quarantine /Applications/awsm.app
```

Handing it to people properly would need a Developer ID and notarisation.
