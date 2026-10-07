# slack-tabbed-tui

Follow hand-picked Slack threads from the terminal, one tab per thread, in
the style of [ghwatch](https://github.com/clobrano/ghwatch).

**Status: early development (M0).** The command line below works, with
sign-in through the browser; the TUI and notifications are next. The plan is in the
[PRD](docs/PRD.md), the choices made so far in [decisions](docs/decisions.md).

## Try it

```sh
go install github.com/clobrano/slack-tabbed-tui/cmd/slack-tabbed-tui@latest
slack-tabbed-tui auth https://acme.slack.com    # sign in (see below)
slack-tabbed-tui add <message link>              # "Copy link" on any message of the thread
slack-tabbed-tui show <message link>             # print the thread
slack-tabbed-tui reply <message link> "on it"    # post a reply
slack-tabbed-tui ls / rm <link>                  # list / stop watching
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
| `internal/slack/slacktest` | fake Slack workspace for tests |
| `internal/creds` | stored sessions, one per workspace |
| `internal/directory` | users, groups and channels, cached on disk |
| `internal/watchlist`, `internal/config` | the watch file and XDG paths (from ghwatch) |
