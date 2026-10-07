# slack-tabbed-tui

Follow hand-picked Slack threads from the terminal, one tab per thread, in
the style of [ghwatch](https://github.com/clobrano/ghwatch).

**Status: early development.** Reading threads live, replying with
`@` (people, bots, groups), `#` and `:` completion, notifications and
the tmux status line work. Next: reactions, edits and files. The plan
is in the [PRD](docs/PRD.md), the choices made so far in
[decisions](docs/decisions.md).

## Quick start

```sh
go install github.com/clobrano/slack-tabbed-tui/cmd/slack-tabbed-tui@latest
slack-tabbed-tui auth https://acme.slack.com    # sign in, in a browser window
slack-tabbed-tui                                 # open the TUI
```

Press `a` and paste a message link ("Copy link" on any message of the
thread): it shows up in a new tab, and new replies appear as they are
posted. The keys you need day to day:

| Key | Action |
| --- | --- |
| `h` / `l` | previous / next thread |
| `j` / `k` | next / previous message |
| `i` | reply |
| `o` | open the thread in Slack |
| `n` | notifications for this thread |
| `a` / `d` | add / remove a thread |
| `?` | all the other keys |
| `q` | quit |

[docs/tui.md](docs/tui.md) describes the screen in detail.

## Command line

```sh
slack-tabbed-tui add <message link>              # watch a thread
slack-tabbed-tui rm <message link>               # stop watching it
slack-tabbed-tui ls                              # list watched threads
slack-tabbed-tui show <message link>             # print a thread, fetched now
slack-tabbed-tui reply <message link> "on it"    # post a reply (or - for stdin)
slack-tabbed-tui status [-json]                  # one-line summary, e.g. for tmux
slack-tabbed-tui -serve                          # run the daemon in the foreground
```

The watched threads are in `~/.config/slack-tabbed-tui/watch`, one link
per line; you can edit the file by hand.

## Notifications and tmux

`n` on a thread turns on desktop notifications (`notify-send`) for it:
new replies and replies that mention you. `N` chooses the events (also:
reactions to your messages) or mutes everything.

```tmux
set -g status-right '#(slack-tabbed-tui status) %H:%M'
```

shows e.g. `💬 ●4 @1`: unread replies and mentions. A trailing `!` means
the data is stale (no daemon, or a workspace logged out).

As in ghwatch, a small background process (the daemon) holds the
connections to Slack for every open TUI. The TUI starts it, and it stops
10 seconds after the last TUI closes; run `slack-tabbed-tui -serve` to
keep notifications and the status line live without a TUI.

## Configuration

Optional, in `~/.config/slack-tabbed-tui/config.toml`:

```toml
interval = "60s"            # how often to fetch threads when live updates are down
browser = "firefox"         # for links; default: $BROWSER, then xdg-open
status_template = '{{if .Unread}}S{{.Unread}}{{end}}'   # over .Threads .Unread .Mentions .Stale

[notify]
backend = "desktop"         # desktop | exec | none
# command = "curl -s -d @- ntfy.sh/my-topic"   # for backend = "exec"

[daemon]
autostart = true
```

### Signing in

slack-tabbed-tui uses your browser session, as the Slack web client does:
no Slack app, nothing to install in the workspace.

`slack-tabbed-tui auth https://acme.slack.com` opens a browser window
(Chromium, Chrome, Brave or Edge, with a profile of its own). Sign in as
usual, SSO and 2FA included; the window closes by itself once you are in.
Without one of those browsers, or with `auth -manual <URL>`, you copy the
`xoxc-` token and the `d` cookie from your own browser instead; `auth`
explains where to find them.

The session goes to the desktop keyring (through `secret-tool`), or to
`~/.config/slack-tabbed-tui/credentials.json` (mode 0600) when there is no
keyring. `$SLACK_TOKEN` and `$SLACK_COOKIE` override it. `auth` with no
argument checks every stored session.

> This is not an official Slack client. Using your session from a
> third-party program may be against your workspace's terms.

## Development

```sh
go test -race ./...
```

Tests run against a fake Slack (`internal/slack/slacktest`) and a fake
browser, no network needed. `STT_TEST_BROWSER=/path/to/chromium` also
runs the sign-in against a real, headless Chromium.

| Path | Contents |
| --- | --- |
| `cmd/slack-tabbed-tui` | entry point and CLI subcommands |
| `internal/slack` | Web API client (browser session), live event stream, thread links, Slack markup to text |
| `internal/ws` | WebSocket client (and server side for fakes) |
| `internal/login` | browser sign-in through the Chrome DevTools protocol |
| `internal/slack/slacktest` | fake Slack workspace for tests (and `docs/demo/fakeslack`) |
| `internal/daemon` | event streams, fetching, unread, notifications, shared state |
| `internal/model` | the snapshot: threads ready to display |
| `internal/complete` | what `@`, `#` and `:` complete to, ranked like Slack |
| `internal/emoji` | Slack's emoji names (generated from iamcal/emoji-data, MIT) |
| `internal/tui` | tabbed TUI (raw terminal, no third-party dependency) |
| `internal/ipc`, `internal/notify`, `internal/browser` | daemon socket, notifiers, links and clipboard (from ghwatch) |
| `internal/creds` | stored sessions, one per workspace |
| `internal/directory` | users, groups and channels, cached on disk |
| `internal/watchlist`, `internal/config` | the watch file and XDG paths (from ghwatch) |
