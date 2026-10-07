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
  token in the form body.
- **Cookies and User-Agent**: the browser sign-in keeps every cookie
  the browser would send to the workspace host, not only `d`, and the
  browser's User-Agent; API calls and the event socket send both.
  Enterprise Grid sessions were rejected (`invalid_auth`) with `d`
  alone: they also need `d-s`. The stored cookie is a whole Cookie
  header; a bare `xoxd-…` value still works (manual sign-in,
  `$SLACK_COOKIE`). A failed check lists the cookie names sent, never
  their values.
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

## Daemon and TUI

- **Same architecture as ghwatch**: one daemon per user owns the Slack
  connections and the state; TUIs talk to it over a Unix socket (NDJSON)
  and get a fresh snapshot after every change. The IPC, lock, spawn,
  notifier, browser and terminal code are ghwatch's, adapted.
- **Fetching**: an event in a watched thread (a new reply, an edit, a
  deletion, a reaction) triggers a fetch of the whole thread with
  `conversations.replies`, after a 300 ms debounce, rather than patching
  the state from the event. One call per burst of events keeps the
  state exact (edits, deletions, reaction counts) with little code.
  After every (re)connection every thread of the workspace is fetched
  again; threads of a workspace without a live connection are fetched
  every `interval` (60 s); everything is re-fetched every 5 minutes as a
  safety net.
- **The snapshot carries the messages**, already rendered (names
  resolved, markup turned into text), so TUIs need no Slack code and
  show the last state at once. The PRD planned bodies in a separate
  cache; one file (mode 0600) is simpler and small for a curated list.
- **Unread** is tracked by the daemon (`read_ts` per thread), shared by
  every client: a newly added thread starts read, your own messages are
  never unread, a thread counts as read after 1.5 s on screen (or `u`),
  and replying marks it read up to your message. Syncing read state with
  Slack is still to do (it needs the web client's thread-mark endpoint).
- **Notifications** come from comparing each fetch with the previous
  one: a new reply from someone else (a mention when it names you,
  @here, @channel or @everyone), and, if enabled, more people reacting
  to one of your messages. Only threads with alerts on (`n`) notify.
- **User groups** are loaded once per daemon start (`usergroups.list`)
  so `@team` mentions show their handle.
- **`enter` opens the selected message in Slack** (as it opens the job
  in ghwatch). The PRD had it expand long messages; messages are always
  shown whole instead.
- **Composer**: plain text is escaped for Slack on send; mentions,
  groups and channels picked from the completion list are kept as
  label → token pairs per draft and swapped in on send (longest label
  first). A mention typed by hand without picking stays plain text, as
  in Slack.
- **Completion** is asked of the daemon (`complete` over the socket,
  one request per keystroke while the word starts with `@`, `#` or
  `:`): the directory can be large (tens of thousands of users on
  Enterprise Grid) and lives in the daemon. Ranking (thread
  participants, channel members, groups, @here/@channel, others; name
  start before word start) is in `internal/complete`, a pure package.
  Channel members are fetched once per channel (up to 1000).
- **Emoji**: names come from iamcal/emoji-data (the set Slack uses),
  turned into a Go table by `internal/emoji/gen`. Standard `:name:` is
  shown as the emoji in messages and reactions; custom emoji stay
  `:name:`. Emoji that need a variation selector (🛳️) may take one cell
  more or less depending on the terminal.
