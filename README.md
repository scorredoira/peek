# peek — browser remote operator

CLI tool to control Chrome via DevTools Protocol. No MCP, no Puppeteer, no intermediaries. Talks directly to Chrome.

## Install

Download the binary for your platform and put it on your `PATH`. Nothing else is
needed — `peek` drives the Chrome you already have.

**macOS (Apple Silicon)**

```bash
curl -fsSL https://prisnap.com/peek/peek-darwin-arm64 -o /usr/local/bin/peek && chmod +x /usr/local/bin/peek
```

**macOS (Intel)**

```bash
curl -fsSL https://prisnap.com/peek/peek-darwin-amd64 -o /usr/local/bin/peek && chmod +x /usr/local/bin/peek
```

**Linux**

```bash
curl -fsSL https://prisnap.com/peek/peek-linux-amd64 -o /usr/local/bin/peek && chmod +x /usr/local/bin/peek
```

**Windows (PowerShell)**

```powershell
curl.exe -fsSL https://prisnap.com/peek/peek-windows-amd64.exe -o peek.exe
```

A binary fetched with `curl` carries no quarantine attribute, so macOS Gatekeeper
never runs and there is nothing to sign or notarize.

From source (needs Go):

```bash
go install github.com/scorredoira/peek@latest
```

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

Resize the browser window.

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

Click an element by its visible text. Also supports `--css` and `--id` flags for DOM-based lookup.

```bash
peek --port $PORT click Login
peek --port $PORT click "Save changes"

# Click by CSS selector (useful for elements not in the accessibility tree)
peek --port $PORT click --css ".grid-cell" "08:36"
peek --port $PORT click --css ".btn-primary"

# Click by DOM id
peek --port $PORT click --id submitBtn
```

When using `--css` or `--id`, an optional text argument filters by text content (case-insensitive substring match).

**It refuses a press a person could not make.** Before anything is dispatched the element is
scrolled into view and checked: disabled (including `aria-disabled` and a disabled
`<fieldset>`), not rendered, invisible, fully transparent, zero-sized, click-through, still
outside the viewport, or covered by something else. Each of those is an error naming what is
in the way, never a `clicked` line.

**And it dispatches what a real press dispatches** — `pointerdown`, `mousedown`, `pointerup`,
`mouseup`, then the native activation. `element.click()` alone fires one `click` event and no
pointer or mouse events at all, so every control that opens on `mousedown` (most custom
dropdowns, menus, drag handles) did nothing while peek printed success.

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
peek --port $PORT select "Booking type" "Green Fee 18"
```

If the dropdown trigger doesn't have a standard input role (textbox, combobox), `select` falls back to finding it by visible text.

#### type

Type raw text into the currently focused element.

```bash
peek --port $PORT type "hello world"
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

Wait for the URL to contain a pattern.

```bash
peek --port $PORT wait --url /admin/dashboard
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

#### dialog accept

Accept the next JavaScript dialog.

```bash
peek --port $PORT dialog accept
peek --port $PORT dialog accept "prompt text"
```

#### dialog dismiss

Dismiss dialogs.

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
