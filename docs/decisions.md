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
- **Session storage**: a JSON file, mode 0600, for now. The PRD asks for
  the OS keyring first; that comes with the browser-driven login.
  `$SLACK_TOKEN` + `$SLACK_COOKIE` override the file.
- **Sign-in, first version**: the token and cookie are pasted by hand
  (the PRD's fallback). `auth` checks them with `auth.test` before
  storing them, and `auth` with no argument re-checks every session and
  reports the ones Slack logged out.
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
