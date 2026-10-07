# slack-tabbed-tui

Follow hand-picked Slack threads from the terminal, one tab per thread, in
the style of [ghwatch](https://github.com/clobrano/ghwatch).

**Status: early development (M0).** The command line below works; the TUI,
live updates and notifications are next. The plan is in the
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
no Slack app, nothing to install in the workspace. For now you copy the
session from the browser by hand; `auth` explains where to find the
`xoxc-` token and the `d` cookie. A browser-driven login (SSO included)
is the next step.

The session is stored in `~/.config/slack-tabbed-tui/credentials.json`
(mode 0600). `$SLACK_TOKEN` and `$SLACK_COOKIE` override it.

> This is not an official Slack client. Using your session from a
> third-party program may be against your workspace's terms.

## Development

```sh
go test -race ./...
```

Tests run against a fake Slack (`internal/slack/slacktest`), no network
needed.

| Path | Contents |
| --- | --- |
| `cmd/slack-tabbed-tui` | entry point and CLI subcommands |
| `internal/slack` | Web API client (browser session), thread links, Slack markup to text |
| `internal/slack/slacktest` | fake Slack workspace for tests |
| `internal/creds` | stored sessions, one per workspace |
| `internal/directory` | users, groups and channels, cached on disk |
| `internal/watchlist`, `internal/config` | the watch file and XDG paths (from ghwatch) |
