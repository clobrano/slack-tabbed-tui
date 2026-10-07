# Decisions

Choices made while implementing the [PRD](PRD.md), in the style of
ghwatch's `docs/decisions.md`.

## Resolved open questions

| Question | Decision | Implementation |
| --- | --- | --- |
| Q4: standard library or modules | Standard library only, as ghwatch | `go.mod` has no requirements. The WebSocket client (live updates and the browser-driven login both need one) will be a small RFC 6455 client in `internal/ws`. |
| Q8: separate binary or ghwatch kind | Separate repository and binary | Generic ghwatch packages are copied and adapted (`watchlist` so far; daemon, IPC, notifier and terminal layer next), not shared as a module. Both projects are MIT and by the same author. |

## Other choices

- **Item ID** is `thread:<workspace>/<channel>/<thread_ts>`, where the
  workspace is its host (`acme.slack.com`). `app.slack.com` links carry a
  team ID instead; it is replaced by the host when a session for that team
  is stored, so every link form of a thread gives the same ID.
- **API host**: calls go to the workspace's own host
  (`https://acme.slack.com/api/…`), as the web client does, with the
  token in the form body and the `d` cookie in the header.
- **Session storage**: the token and cookie go to the desktop keyring
  through libsecret's `secret-tool` (no D-Bus code in the binary, as
  ghwatch uses `notify-send`). The credentials file keeps the rest
  (workspace, team, user). Without `secret-tool` or a D-Bus session, or
  when the keyring refuses, the file (mode 0600) holds the secrets too.
  `$SLACK_TOKEN` + `$SLACK_COOKIE` override both.
- **Sign-in (Q11)**: `auth <URL>` starts a Chromium-based browser with a
  profile of its own (`$XDG_STATE_HOME/slack-tabbed-tui/browser`, kept so
  SSO remembers you next time) and the DevTools protocol on a random
  port. Every second it reads the `d` cookie (`Storage.getCookies`) and,
  in each Slack page, the web client's `localStorage.localConfig_v2`,
  which lists the signed-in teams with their `xoxc-` tokens. Once both
  are there it closes the browser. It never touches the user's own
  browser profiles. Firefox is not supported: it no longer speaks the
  DevTools protocol. `auth -manual` (and no browser found) falls back to
  pasting the token and cookie. Either way, `auth.test` checks the
  session before it is stored.
- **Live updates**: `rtm.connect` and the WebSocket URL it returns, with
  the `d` cookie in the handshake. The stream pings every 30 s, treats
  60 s of silence as a dead connection, reconnects with backoff (1 s to
  5 min, honouring `Retry-After`), and emits `stt_connected` after every
  connection so the daemon re-fetches what it may have missed. An
  invalid session ends the stream (logged out) instead of retrying.
- **One session, any workspace**: with a single stored session, links to
  any host use it. Enterprise Grid links use the org host
  (`acme.enterprise.slack.com`), which `auth.test` does not report.
- **Names**: users, channels and groups are looked up on demand
  (`users.info`, `conversations.info`) and cached per team in
  `$XDG_CACHE_HOME/slack-tabbed-tui/<team>/directory.json`. The full
  `users.list` sync (for `@` completion) is implemented but not used by
  the CLI.
- **Outgoing text** is escaped (`&`, `<`, `>`) as Slack requires. Typed
  `@name` mentions are not converted to `<@U…>` yet: that comes with
  the composer and its completion.
