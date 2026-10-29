# peek — browser remote operator

CLI tool to control Chrome via DevTools Protocol. No MCP, no Puppeteer, no intermediaries. Talks directly to Chrome.

## Install

Download the binary for your platform and put it on your `PATH`. Nothing else is
needed — `peek` drives the Chrome you already have.

**macOS (Apple Silicon)**

```bash
curl -fsSL https://prisnap.com/dl/peek-darwin-arm64 -o peek && chmod +x peek
sudo mv peek /usr/local/bin/
```

**macOS (Intel)**

```bash
curl -fsSL https://prisnap.com/dl/peek-darwin-amd64 -o peek && chmod +x peek
sudo mv peek /usr/local/bin/
```

**Linux**

```bash
curl -fsSL https://prisnap.com/dl/peek-linux-amd64 -o peek && chmod +x peek
sudo mv peek /usr/local/bin/
```

**Windows (PowerShell)**

```powershell
curl.exe -fsSL https://prisnap.com/dl/peek-windows-amd64.exe -o peek.exe
```

The download and the move are two steps on purpose: `/usr/local/bin` belongs to root,
so writing there directly fails with `curl: (56) Failure writing output to destination`
— which reads like a network problem and is not one. No `sudo`? Skip the move and put
`peek` anywhere on your PATH you can write to.

A binary fetched with `curl` carries no quarantine attribute, so macOS Gatekeeper
never runs and there is nothing to sign or notarize.

From source (needs Go):

```bash
go build -o ~/go/bin/peek .
```

The repo is private, so Go needs to know to fetch it with git rather than through the public
proxy, once per machine:

```bash
go env -w GOPRIVATE=github.com/scorredoira/*
git config --global url."git@github.com:scorredoira/".insteadOf "https://github.com/scorredoira/"
```

`browse/` is the Chrome driving shared with scl: `scl check`, `scl kill` and the engine's
browser tests import it from here. A change to it reaches scl when scl takes the new version:
`go get github.com/scorredoira/peek@<commit>` in scl, then `go mod tidy`.

## Quick Start

Single session (most common):

```bash
peek open http://localhost:9092/admin/login
peek fill Email admin@demo.com
peek fill Password 123
peek click Login
peek close
```

Multiple parallel sessions (each gets its own isolated Chrome):

```bash
PORT=$(peek open http://localhost:9092/admin/login)
peek --port $PORT fill Email admin@demo.com
peek --port $PORT click Login
peek --port $PORT close
```

## Usage

```
peek [--port PORT] [--headless] [-w N] <command> [args...]
```

Default port: `9222`. Use `--headless` for headless mode (no browser window). When running multiple sessions, `peek open` auto-picks the next free port and prints it.

---

## Commands

### Chrome

#### open

Launch a new Chrome instance and optionally navigate to a URL. Prints the port number to stdout. Each instance is fully isolated (separate profile, cookies, storage).

```bash
peek open                                     # launch Chrome, print port
peek open http://localhost:9092/admin/login   # launch and navigate
peek --headless open http://example.com       # headless mode (no window)
```

If a Chrome is already running on the default port (or the one specified with `--port`), prints that port.

Works on macOS, Linux, and Windows. Auto-detects Chrome location.

#### close

Kill the Chrome instance on the given port.

```bash
peek close                    # close Chrome on default port 9222
peek --port 9223 close        # close Chrome on port 9223
```

#### vault (macOS)

A Chrome profile that keeps its logins, for the sites with no API (App Store Connect, Play
Console, Google Cloud…). It lives in an AES-256 encrypted disk image in
`~/.config/peek/vaults/<name>.sparsebundle`. While locked nothing can drive it.

```bash
peek vault new main          # asks for the password twice (unlock also creates it)
peek unlock                  # main: asks for it, opens a visible Chrome, prints its port
peek unlock apple <url>      # another vault, opened on a page
peek restart                 # main's Chrome closed and started again, still unlocked
peek lock                    # closes main's Chrome and locks it
peek status                  # each vault, locked or unlocked, and its port
```

The password is read from the terminal, never from an argument or stdin, so a process with no
terminal (an agent's shell) cannot unlock. Closing the vault's last window locks it too. The
person signs in to each site by hand once; the logins stay in the vault.

**Recipes**: how a task is done on a site with no API (the addresses, what to press, what the
page answers when it worked) is kept in the vault, in `recipes/<name>.md`, next to the logins
it needs. Any agent driving the vault reads it there, and only while it is unlocked. Whoever
does a task for the first time saves its recipe, and fixes it when the site has changed. A
password never goes in one.

```bash
peek recipes                         # main's recipes, each with its first line
peek recipe ionos-mailbox-password   # print one (another vault: apple/sign-in)
peek recipe ionos-mailbox-password --save < recipe.md   # write it, replacing it
```

- **Orange, main in green**: every Chrome peek opens with a window, the vaults' too, has an
  orange frame and bar, so it is told from the person's own at a glance; the main vault's is
  green, so it is told from the rest. The page is not touched, nor any screenshot.
- **A person's pace, when asked**: `peek unlock [name] --paced` (until it is locked) or
  `peek --paced <command>` makes every press and keystroke (click, fill, select, type, press,
  hover, drag, upload, scroll) wait half a second or more first, never the same: for the sites
  that would rather not be driven by a machine. Paced, everything goes through Chrome's own
  input (trusted events), shaped on a recording of a real hand: the pointer travels from where
  the last command left it, nearly straight, fast first and homing in slowly, for as long as
  the distance and the target's size ask (Fitts); the press lands off-centre and is held a
  tenth of a second; fill and type go key by key, held keys overlapping, Shift and the Spanish
  dead accent pressed as keys, and now and then a neighbouring letter typed and taken back;
  an element out of view and `peek scroll` come by trackpad flicks with an inertial tail.
  Otherwise peek goes as fast as it can.
- **A session that ended**: `navigate` to a page that lands on a sign-in page instead
  (accounts.google.com, idmsa.apple.com, a site's /login) fails and says so: sign in by hand
  in the window, then go on.

---

### Navigation

#### navigate (alias: nav)

Go to a URL. Waits for the page to load, then prints where it landed.

```bash
peek --port $PORT navigate http://localhost:9092/admin/login
peek --port $PORT nav http://example.com
```

**A page that answered 4xx or 5xx is an error, not a URL.** A 404 or a 500 navigates
perfectly — the document loads and the URL prints — so without this every screenshot after
it is of the server's error page and nothing said so. `reload` reports the same way.

#### reload

Reload the current page.

```bash
peek --port $PORT reload
```

#### back

Go back in browser history.

```bash
peek --port $PORT back
```

#### forward

Go forward in browser history.

```bash
peek --port $PORT forward
```

#### resize

Resize the browser window and set the page viewport to the exact requested CSS dimensions,
including phone widths below the desktop window minimum.

```bash
peek --port $PORT resize 1280 720
peek --port $PORT resize 375 812    # iPhone X
peek --port $PORT resize 1920 1080  # Full HD
```

---

### Inspection

#### snapshot (alias: snap)

Print the accessibility tree. This is how you find elements to interact with.

```bash
peek --port $PORT snapshot
peek --port $PORT snap --verbose
```

Output:

```
[4] RootWebArea "Login"
[12] textbox "Email" value=""
[15] textbox "Password" value=""
[18] button "Login"
```

Use the text shown in quotes to target elements with `click`, `fill`, etc.

#### screenshot (alias: ss)

Take a screenshot. Defaults to `/tmp/peek.jpg` (`/tmp/peek.png` for a PNG).

```bash
peek --port $PORT screenshot
peek --port $PORT ss /tmp/login-page.png
peek --port $PORT screenshot --full /tmp/full-page.png
peek --port $PORT screenshot --css ".DataList" --scale 3 /tmp/list.png
peek --port $PORT screenshot --clip 40,120,600,300 --scale 2 /tmp/detail.png
```

Options:
- `--full` — capture the entire page (scroll included)
- `--css <sel>` — capture only that element, scrolled into view
- `--clip x,y,w,h` — capture that region of the viewport, in CSS pixels
- `--scale N` — re-render the region at N times the size
- `--png` — PNG instead of JPEG
- `--quality N` — JPEG quality (default 80)

A capture does not touch the page it photographs: the tab is brought to the
front, which is what makes Chrome draw it, and nothing else changes — so a
popup, a menu or an open select is still there when the shutter falls. The
region is read from the viewport at whatever scroll the page is at (`--clip`,
and the element's own box for `--css`).

What reaches PAST the window is the exception — `--full`, or `--css` on an
element taller than it. Chrome draws that only by resizing the page, so
everything that closes on resize is gone from those pictures; peek says so on
stderr each time it has to ask.

`--css` and `--clip` are exclusive, and neither combines with `--full`. Chrome
re-renders the region at the requested scale rather than upscaling the raster,
so small text stays sharp — that is what makes this a zoom and not a crop of a
blurry picture. Any capture that crops or scales is written as PNG, because
JPEG artifacts are exactly what you would be looking at up close.

#### url

Print the current page URL.

```bash
peek --port $PORT url
```

#### html

Print the page HTML source.

```bash
peek --port $PORT html
peek --port $PORT html > /tmp/page.html
```

#### styles (alias: style)

Print how an element is actually painted: its box, resolved colors and font, and the
four ways it can be there without being readable.

```bash
peek --port $PORT styles --css ".PageHeader h1"
peek --port $PORT styles "Save"
```

```
tag         h1
classes     PageHeader-title
box         525x127 at 74,116
text        Bookings
display     block
visibility  visible
opacity     1
...
clipped:   its content overflows the box and is being cut off
```

The warnings at the end are the point — `hidden`, `empty` (a zero-size box),
`offScreen`, and `clipped` (the text is genuinely cut off, not merely spilling out of
a box that does not clip). A screenshot cannot tell you any of them, and every
affirmative line peek prints implies there is nothing left to check.

---

### Interaction

#### click

Click an element by its visible text. Also supports `--css` and `--id` flags for DOM-based lookup, and `--ref N` for the node a snapshot printed as `[N]` (`--id` is the DOM id attribute, not that number).

```bash
peek --port $PORT click Login
peek --port $PORT click "Save changes"

# Click by CSS selector (useful for elements not in the accessibility tree)
peek --port $PORT click --css ".grid-cell" "08:36"
peek --port $PORT click --css ".btn-primary"

# Click by DOM id
peek --port $PORT click --id submitBtn
peek --port $PORT click --ref 2724   # the [2724] a snapshot printed
peek --port $PORT click --role tab "Pages"   # the tab, not a link with the same text
```

When using `--css` or `--id`, an optional text argument filters by text content (case-insensitive substring match).

**Two drawn things that say the text are refused, never guessed** — a "Delete" on every row
names none of them. The error lists each with its `[ref]`, and offers `--role` when they are of
different kinds (a link and a tab). A hidden twin (a menu folded away) is not counted, nor a
link around a button that says the same.

**It says what the page answered.** Under the `clicked` line (and `dblclick`, `select`, and
`press Enter`/`Space`) come what the page announced (a toast, an alert, a status line in a live
region — even one gone before the click returned), the fields it marked invalid with their
message, a confirm and how it was answered, the address it went to and a tab it opened. It
waits for the page to be quiet first (no request of its own on the way, no document loading),
up to 5 seconds:

```
clicked "Save"
  the page said "Record saved"
clicked "Send"
  "Email" is marked invalid: "Required"
```

**It refuses a press a person could not make.** Before anything is dispatched the element is
scrolled into view and checked: disabled (including `aria-disabled` and a disabled
`<fieldset>`), not rendered, invisible, fully transparent, zero-sized, click-through, still
outside the viewport, or covered by something else. It waits up to 3 seconds for that to pass
(a menu fading in is transparent for its first frames); after that, each is an error naming
what is in the way, never a `clicked` line. A transparent one names the element it measured
and the animations running on it. A button the page draws again while it is being
pressed (a bar painted anew) is found again and the one on screen is pressed.

**And it dispatches what a real press dispatches** — `pointerdown`, `mousedown`, `pointerup`,
`mouseup`, then the native activation. `element.click()` alone fires one `click` event and no
pointer or mouse events at all, so every control that opens on `mousedown` (most custom
dropdowns, menus, drag handles) did nothing while peek printed success. An element with no
native `click()` — an `<svg>`, which is what an icon button is — gets the `click` event
dispatched instead, so the press is never left halfway.

The same gate covers `rightclick`, `dblclick` and `hover`. Hovering a disabled control is
allowed — that is how its tooltip is read — so only being unseeable or covered refuses there.

#### dblclick

Double-click an element. Supports `--css` and `--id` flags.

```bash
peek --port $PORT dblclick "row content"
peek --port $PORT dblclick --css ".editable-cell" "Total"
```

#### fill

Fill an input field. First argument is the label text, rest is the value. Matches by accessible name: `<label>`, `aria-label`, or **placeholder text**.

```bash
peek --port $PORT fill Email admin@demo.com
peek --port $PORT fill Password 123
peek --port $PORT fill "First name" Santiago
peek --port $PORT fill --name email admin@demo.com   # by the name attribute
peek --port $PORT fill --css "#search" bookings      # by selector
```

Zero-width characters in placeholder text are stripped automatically for matching.

**It reads the value back, and says what the field really holds.** Three outcomes:

- The value landed as typed — `filled "Email" = "admin@demo.com"`.
- The widget reformatted it — `filled "Total" = "1000" — the field shows "1.000,00 €"`.
  That is the widget working, so it succeeds; you are told because it may not be what
  you meant.
- Nothing landed — the field is still empty, and that is an error, not a success.

A **disabled, readonly or invisible** field is refused before anything is typed. Typing
into one is a no-op that used to print `filled` and leave the next step building on a
value that was never set.

#### select

Select a dropdown option. Works with native `<select>` and **custom widget dropdowns**. For custom widgets, it clicks to open the dropdown, waits for options to render, then clicks the matching option.

```bash
peek --port $PORT select Country Spain
peek --port $PORT select "Leave type" Vacation

# Works with custom widget dropdowns (e.g. React Select, S.Select)
peek --port $PORT select "Service" "Green Fee 18"
```

A native `<select>` matches an option by its text — no-break spaces and accents read as typed — or by its value (`select "Idioma principal" en-US`).

If the dropdown trigger doesn't have a standard input role (textbox, combobox), `select` falls back to finding it by visible text.

**It refuses a select nobody could use**, the same gate as `click`, and it names the
options that exist when the one you asked for does not:

```
peek: option "Portugal" not found
  available: [Spain France Italy]
```

A custom dropdown is opened with the full pointer sequence, so one that opens on
`mousedown` actually opens — a bare `click()` left it shut while the command reported
success. A real `<select>` is matched and set in JS rather than through rod's Select,
which goes through the scroll-into-view path that hangs on complex layouts.

#### type

Type raw text into the currently focused element.

```bash
peek --port $PORT type "hello world"
peek --port $PORT type --stdin < pw   # a password: never in the process list nor the output
```

#### press

Press a keyboard key.

```bash
peek --port $PORT press Enter
peek --port $PORT press Tab
peek --port $PORT press Escape
```

Supported keys: `Enter`, `Tab`, `Escape`/`Esc`, `Backspace`, `Delete`, `ArrowUp`/`Up`, `ArrowDown`/`Down`, `ArrowLeft`/`Left`, `ArrowRight`/`Right`, `Space`, `Home`, `End`, `PageUp`, `PageDown`.

#### hover

Hover over an element. Supports `--css` and `--id` flags.

```bash
peek --port $PORT hover "Settings"
peek --port $PORT hover --css ".menu-item" "Reports"
```

#### scroll

Scroll the page down by that many pixels, or up when negative; a screen by default. Paced, it
is trackpad flicks with the pointer where it rests.

```bash
peek --port $PORT scroll          # a screen down
peek --port $PORT scroll -400     # 400px up
```

#### drag

Drag one element to another.

```bash
peek --port $PORT drag "Item 1" "Drop zone"
```

#### upload

Upload a file to a file input. Uses a CSS selector (not text).

```bash
peek --port $PORT upload "input[type=file]" /path/to/document.pdf
```

---

### Waits

#### wait

Wait for text or an element to appear on the page (default timeout: 10s).

```bash
peek --port $PORT wait Dashboard
peek --port $PORT wait "Record saved"
peek --port $PORT wait --timeout 30s "Processing complete"
peek --port $PORT wait --css ".DataList tbody tr"      # an element
peek --port $PORT wait --css ".Total" "1.240,00"       # text inside one
```

Text is looked for in the accessibility tree **and** in the page's rendered text, plus
the value of every input. So a table cell, a badge, a plain `<span>` and what you just
typed into a field all count as on screen — the tree alone missed all of those.

#### wait --gone

Wait for text or an element to **disappear**.

```bash
peek --port $PORT wait --gone "Loading..."
peek --port $PORT wait --gone --css ".Spinner"
```

**It has to see the thing first.** If what you name was never there, the command fails
saying so rather than returning instantly. Otherwise the usual idiom — click Save, wait
for the spinner to go — passes before the spinner has even rendered, and waiting for
nothing reads as proof the operation finished.

#### wait --url

Wait for the URL to contain a piece of text, or with `--url-re` to match a regular expression
(an address with an id in it).

```bash
peek --port $PORT wait --url /admin/dashboard
peek wait --url-re '/apps/[0-9]+/'
```

---

### Tabs

#### pages

List all open tabs. The `*` marks the one every command acts on.

```bash
peek --port $PORT pages
```

```
  [0] https://example.com/popup
* [1] https://example.com/
```

#### page

Switch to a tab by its index. **The choice is remembered** — every later command acts on it
until you switch again or close it.

```bash
peek --port $PORT page 0
```

Which tab peek drives used to be re-derived from the tail of Chrome's target list on every
single command, so a click that opened a popup silently moved everything onto the popup — or
failed to, equally invisibly — and the form you thought you were verifying was in the other
tab. `peek open` now names its tab, `newpage` claims the tab it creates, and a popup the page
opened by itself never steals the session.

#### newpage

Open a new tab.

```bash
peek --port $PORT newpage http://localhost:9092/admin
```

#### closepage

Close the current tab.

```bash
peek --port $PORT closepage
```

---

### JavaScript

#### js

Evaluate arbitrary JavaScript. Promises are automatically awaited.

```bash
peek --port $PORT js "document.title"
peek --port $PORT js "document.querySelectorAll('button').length"

# Promises are awaited automatically
peek --port $PORT js "fetch('/api/status').then(r => r.json())"
```

---

### Debug

#### console

Show what the page said about itself. The capture is installed by every command, before the
page's own scripts run.

```bash
peek --port $PORT console
```

It carries more than `console.log`: uncaught exceptions, unhandled promise rejections, fetch
and XHR calls that failed with their status, CSP violations, and a `<script>`, `<img>` or
`<link>` that failed to load — that last family fires on the element and never bubbles, so a
missing bundle or a 404'd stylesheet used to be invisible everywhere and the page just
rendered unstyled.

**Errors survive a navigation.** The log lives in the page, so a full page load wiped it:
sign in, land on the next screen, and the exception that broke the sign-in page was gone.
Errors are now carried across in the tab's session storage.

#### network (alias: net)

Show recent network requests **with their HTTP status**, and count the failures.

```bash
peek --port $PORT network
```

```
200       143ms  css      22.8KB  https://example.com/styles.css
404 !     49ms   fetch    526B    https://example.com/api/rows
BLOCKED   0ms    fetch    0B      http://127.0.0.1:1/dead

1 of these requests FAILED — a screen can render perfectly on failed data
```

`BLOCKED` is a request that never got a response at all: refused by CORS or CSP, or a
dead host. When nothing failed it says so, so an empty-looking screen with a healthy
list points you elsewhere.

---

### Dialogs

**A dialog never blocks the page, and you do not have to do anything.** `alert`, `confirm` and
`prompt` are answered inside the page, which is also what lets `peek dialog` tell you what was
asked. The default answer is **no**: pressing OK on "Delete everything?" while nobody is looking
is not a tool's call.

What the page's own overrides cannot reach is answered by Chrome instead, and said on stderr:

| Dialog | Answer | Why |
|--------|--------|-----|
| `beforeunload` — "Leave site? Changes you made may not be saved" | **accepted** | It is not a function the page calls, so there is nothing to override: only Chrome can answer it. And it is not a question about the app — the command it interrupted is the one that asked to leave, and the unsaved edits are the ones this run typed. |
| `alert` | **accepted** | One button. Accept and dismiss both close it. |
| a `confirm` the overrides never reached — an iframe, or a script that asked first | **dismissed** | The same answer the page's own override gives, so which door it came through does not change what the app was told. |

```
dialog: beforeunload "" → accepted
```

Filling a form and then navigating away was enough to freeze the browser on that one, with every
command after it timing out and nothing but a human at the keyboard to get it moving again.

#### dialog accept

Answer yes from now on — to the confirms and prompts, and to the `prompt` text.

```bash
peek --port $PORT dialog accept
peek --port $PORT dialog accept "prompt text"
```

The answer is remembered in the tab, so it survives the command that set it. The native backstop
in the table above keeps its own copy, and that one only lasts the process — which shows up only
for a `confirm` that reached Chrome instead of the page.

#### dialog dismiss

Back to answering no.

```bash
peek --port $PORT dialog dismiss
```

#### dialog

With no argument, show what the page has actually asked.

```bash
peek --port $PORT dialog
```

```
confirm: Delete this invoice? → dismissed
prompt: New name? → Santiago
```

A `beforeunload` is not in that list: while it is up the renderer is blocked, so there is no
JavaScript left to record it with. Its line on stderr is the record.

**Dialogs are answered by default, and the default answer is no.** A native
`alert`/`confirm`/`prompt` blocks the renderer and a CLI has nobody there to press a button:
the page freezes and the next command times out complaining about something unrelated. So
peek answers them in the page and records them. Auto-accepting would press OK on "Delete
everything?" while nobody was looking, which is why `dialog accept` is something you say
explicitly. The choice is remembered for the tab, so setting it once holds for the commands
that follow.

Reading the log back is the other half: setting an answer proves nothing, and `peek dialog`
is how you check the confirm before a delete was really there.

---

### Testing

#### test

Run `.peek` test files. Each test launches its own isolated Chrome, executes commands in order, and reports pass/fail.

```bash
peek test tests/                          # run all .peek files in directory (recursive)
peek test tests/login.peek                 # run a single test
peek --headless test tests/               # headless mode (no browser window)
peek -w 4 --headless test tests/          # 4 tests in parallel
```

#### shots, compare

A set of screenshots of a back office, one PNG per screen, and the comparison of two sets.
How to use them: `docs/BROWSER_TESTING.md`, "Before and after: every screen".

```bash
peek --port $PORT shots shots/before      # every screen of the signed-in back office
peek compare shots/before shots/after shots/diff   # report.md lists what differs
```

---

## Test Files

Test files use the `.peek` extension. Each file is a sequence of peek commands, one per line:

```
# Login with valid credentials

open http://localhost:9092/admin/login
fill Email admin@demo.com
fill Password 123
click Login

assert url /admin/dashboard
assert text Dashboard
assert gone "Invalid credentials"
```

Rules:
- Lines starting with `#` are comments — the first one is the test name
- Blank lines are ignored
- Each line is a peek command (same syntax as the CLI)
- `assert` commands verify conditions with automatic retry (default timeout: 10s)

### Assert Commands

| Command | What it checks |
|---------|---------------|
| `assert url <pattern>` | URL contains pattern |
| `assert text <text>` | Text is visible on the page |
| `assert gone <text>` | Text is NOT on the page |
| `assert title <text>` | Page title contains text |
| `assert js <expression>` | JavaScript expression returns truthy |
| `assert clean` | The page has reported no errors up to this point |

Assertions retry automatically until they pass or timeout. No need for explicit waits or sleeps between actions and assertions.

**A test fails if the page reported an error, whether or not you asserted it.** Passing used
to mean only "no command returned an error" — a test could navigate, click through a form and
pass with a stack trace behind it and every data call at 500. The captured errors are printed
under the failure. `assert clean` is for saying WHERE: put it after the step you suspect and
the failure names that line instead of the last one.

`start :PORT ...` **refuses a server this run did not start.** Sharing one server between
parallel test files is the point and still works; what is refused is a leftover from a
previous run, which is very likely a previous build — reusing it silently is how a whole
suite goes green against code that was never run.

Override the default 10s timeout:

```
click "Generate report"
assert --timeout 30s text "Report ready"
```

### Background Servers

`start` ensures a server is running before the test continues. If the port is already open, it's a no-op. Otherwise it launches the command and waits up to 30s for the port to accept HTTP connections.

```
start :3000 node server.js
start :9092 go run ./cmd/server
open http://localhost:3000
```

### Shell Commands

`exec` runs a shell command inside a test. Stdout is captured in `${result}` (trimmed).

```
# Read a token from the database and use it
exec mysql -N -s -e "SELECT token FROM s_main.accounts WHERE email='user@test.com'"
navigate http://localhost:9092/reset-password?token=${result}
```

Rules:
- Fails the test if exit code ≠ 0
- Stdout is always captured in `${result}` (overwrites previous value)
- `${result}` is expanded in all subsequent lines
- Use `exec --as VARNAME` for named variables: `exec --as TOKEN mysql ...` → `${TOKEN}`
- `exec` runs before Chrome is launched — useful for setup (creating users, cleaning DB)

### Examples

Login flow:

```
# Login with valid credentials

open http://localhost:9092/admin/login
fill Email admin@demo.com
fill Password 123
click Login

assert url /admin/dashboard
assert text Dashboard
```

Failed login:

```
# Login with wrong password shows error

open http://localhost:9092/admin/login
fill Email admin@demo.com
fill Password wrong
click Login

assert url /admin/login
assert text "Invalid credentials"
assert gone Dashboard
```

Form submission:

```
# Create a new user

open http://localhost:9092/admin/users
click "New user"

assert url /admin/users/new
fill "First name" Santiago
fill "Last name" Test
fill Email test@example.com
select Role Admin
click Save

assert text "User created"
```

JavaScript assertion:

```
# Page has no console errors

open http://localhost:9092/admin/dashboard
assert js "document.querySelectorAll('.error').length === 0"
assert js "document.title.includes('Dashboard')"
```

### Output

```
PASS  login_ok.peek — Login with valid credentials (1.2s)
PASS  create_user.peek — Create a new user (3.4s)
FAIL  delete_record.peek:12 — assert text: "Record deleted" not found

3 tests, 2 passed, 1 failed (4.8s)
```

---

## Multiple Sessions

Each `peek open` launches a separate Chrome instance with its own port and profile directory. Sessions are fully isolated — no shared cookies, localStorage, or state.

```bash
# Terminal 1
PORT=$(peek open http://localhost:9092/admin/login)
peek --port $PORT fill Email admin@demo.com
peek --port $PORT click Login
peek --port $PORT close

# Terminal 2 (runs in parallel, completely isolated)
PORT=$(peek open http://localhost:9092/admin/login)
peek --port $PORT fill Email other@demo.com
peek --port $PORT click Login
peek --port $PORT close
```

## How It Works

- `peek open` launches Chrome with `--remote-debugging-port` and a unique `--user-data-dir` per port
- All commands connect via WebSocket to Chrome's DevTools Protocol, act, and exit
- Element lookup uses Chrome's **accessibility tree** by default — reliable with any framework
- `--css` and `--id` flags use DOM selectors as an alternative for elements not in the AX tree
- Interactive elements (buttons, links) are prioritized over static text when names match
- Zero-width characters in element names/placeholders are stripped for robust matching
- `peek js` uses `Runtime.evaluate` with `awaitPromise` so Promises resolve automatically
- `peek close` sends a close command via CDP to cleanly shut down the Chrome instance

Built on [Rod](https://github.com/go-rod/rod).
