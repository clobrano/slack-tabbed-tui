# PRD: slack-tabbed-tui

Status: **draft for discussion** · Owner: @clobrano · Last update: 2026-10-07

A terminal app to follow a hand-picked set of Slack threads, one tab per
thread, with the look, keys and architecture of
[ghwatch](https://github.com/clobrano/ghwatch). In each thread you can do
what you can do in Slack: read, reply, @-mention people, bots and groups,
react, edit, delete and attach files.

Open questions are marked **Q#** and collected in [§11](#11-open-questions).

---

## 1. Problem

Threads are where work gets done in Slack, but Slack is a poor tool for
following *a few specific* threads:

- The "Threads" view mixes every thread you were ever in, with no way to
  pin the five that matter this week.
- "Get notified about new replies" is all or nothing, and the notification
  is lost among channel noise.
- Context switching: leaving the terminal to check a thread, then losing
  your place in the code.

ghwatch fixed the same problem for PR CI: a curated list, one tab each,
notifications only where you asked for them, and a tmux status line.
slack-tabbed-tui does this for Slack threads, and lets you **take part in
the thread** without leaving the terminal.

## 2. Goals and non-goals

### Goals

1. **Curated watchlist.** Threads are only added by hand: `a` and a
   permalink, or `slack-tabbed-tui add <URL>`. Nothing is added
   automatically (as in ghwatch, see its decisions doc).
2. **Feature parity inside a thread.** Everything you can do in a Slack
   thread pane you can do here, as far as Slack's web client allows
   ([§5](#5-feasibility) says exactly how far). In particular, `@` lists
   every handle, **including bots, apps and user groups**, ranked as Slack
   ranks them.
3. **ghwatch look and keys.** Same colors, tab bar, footer, help and
   settings panels, same keys for the same actions. Someone who uses
   ghwatch already knows how to use it.
4. **Near-real-time.** A new reply shows up within seconds, with no
   polling in the normal case.
5. **Notifications and status line.** Per-thread desktop notifications
   (and `exec` backend), and a tmux status line with unread counts.
6. **Runs with no public server.** A single binary on the user's machine,
   no hosted backend.

### Non-goals (v1)

- A full Slack client: no channel browser, no DMs list, no workspace
  sidebar. A DM or group DM thread *can* be watched if you have its link.
- Huddles, calls, canvases, lists, workflows, Slack AI.
- Being a bot: the app acts **as you**, with your permissions.
- Windows (same stance as ghwatch: Linux first, macOS/BSD build).

## 3. Users and stories

Primary user: a developer living in tmux/terminal, in one or a few
workspaces (possibly Enterprise Grid, possibly with Slack Connect channels).

| # | As a user I want to… | Priority |
| --- | --- | --- |
| U1 | paste a thread link and see it in a new tab | P0 |
| U2 | read the whole thread, with names, times, formatting, reactions, files and edits | P0 |
| U3 | see new replies appear live and which ones I haven't read | P0 |
| U4 | reply, with `@` completion for people, bots and groups, `#` for channels and `:` for emoji | P0 |
| U5 | get a desktop notification when someone replies, or only when I am mentioned | P0 |
| U6 | react to a message, edit or delete my own messages | P1 |
| U7 | "also send to channel" when replying | P1 |
| U8 | attach a file, open/download attachments | P1 |
| U9 | open the thread or a message in Slack (app or browser), copy its link | P0 |
| U10 | see unread counts in tmux | P1 |
| U11 | watch threads from more than one workspace | P1 |
| U12 | search inside the current thread | P2 |

## 4. User experience

### 4.1 Screen

Same layout as ghwatch (`docs/tui.md` there), top to bottom:

- **Title bar:** app name, number of watched threads, how many have unread
  replies and mentions, mute state, connection state (`live` over the
  WebSocket, `polling`, `logged out`, or yellow `last sync … ago` when failing).
- **Tabs:** one per thread, in the order added: `N:` number, an unread
  marker (`●3`, or `@` when you are mentioned), `#channel` and a short
  name (first words of the parent message, or a user-given alias), and a
  bell when alerts are on. A thread from a non-default workspace gets a
  short workspace prefix, like ghwatch's repository prefix.
- **Thread header:** channel, workspace, parent author and time,
  participants (avatars replaced by names), reply count, last reply time.
- **Message list:** a bordered box. The parent message first, then
  replies, oldest at the top. Each message: author (bots marked `APP`),
  time (`(edited)` when edited), body, attachments, reactions as chips
  (`👍 3` highlighted when one is yours). A divider marks "new since you
  last looked". The selected message is the full-width highlight bar.
- **Composer:** hidden until you start a reply; then a multi-line box at
  the bottom with the completion popup above it.
- **Footer:** main keys, then `? all keys`; messages replace hints for a few
  seconds, as in ghwatch.

Colors, icon sets (`fancy` / `safe` / ASCII), `NO_COLOR` behavior and
panels are taken from ghwatch unchanged.

### 4.2 Keys

Same key → same meaning as ghwatch where the action exists.

| Key | Action | ghwatch equivalent |
| --- | --- | --- |
| `h` / `l`, `gT` / `gt` | previous / next thread | previous / next PR |
| `1`–`9` | jump to tab N | same |
| `j` / `k`, `gg` / `G` | next / previous message, first / last | jobs |
| `ctrl+d` / `ctrl+u` | half page down / up | — |
| `/` | find in the current thread | find a job |
| `enter` | expand / collapse the selected message (long or with attachments) | open job |
| `o` | open the thread in Slack (desktop app via `slack://`, else browser) | open PR |
| `O` | open the selected message's attachment / link | — |
| `y` / `Y` | copy thread link / selected message link | same |
| `i` | reply (open the composer) | — |
| `e` | edit selected message (yours only) | — |
| `x` | delete selected message (yours only, asks to confirm) | — |
| `+` | add a reaction to the selected message (emoji picker) | — |
| `-` | remove one of your reactions | — |
| `u` | mark thread read (synced with Slack) | — |
| `n` | toggle notifications for the current thread | same |
| `N` | notification settings (event types, global mute) | same |
| `a` / `d` | add / unwatch a thread | same |
| `r` | sync now | poll now |
| `?` | all keys | same |
| `q` | quit (the daemon keeps running) | same |

**Composer keys:** `enter` sends, `alt+enter` / `ctrl+j` new line
(a plain terminal can't tell `shift+enter` from `enter`; with the kitty
keyboard protocol `shift+enter` works too), `ctrl+e` edits the draft in
`$EDITOR`, `ctrl+b` toggles "also send to #channel", `ctrl+a` attaches a
file, `esc` closes the composer and keeps the draft, `ctrl+c` discards it.
In the completion popup: `tab` / `ctrl+n` / `ctrl+p` / arrows move,
`enter` or `tab` accepts, `esc` closes.

Drafts are kept per thread across restarts, like Slack.

### 4.3 Completion

| Trigger | Lists | Inserted as (sent to Slack) |
| --- | --- | --- |
| `@` | users, **bots and app users**, user groups, `@here` `@channel` `@everyone` | `<@U…>`, `<!subteam^S…>`, `<!here>` … |
| `#` | channels you can see | `<#C…>` |
| `:` | standard + workspace custom emoji, with aliases | `:name:` |

Matching like Slack: prefix of display name, real name, handle and (for
groups) group handle; case and accent insensitive. Ranking: thread
participants first, then members of the thread's channel, then everyone
else; deactivated users are hidden; bots show an `APP` tag and their app
name. Mentions show as `@Name` in the composer and are converted to IDs on
send, so renaming in the composer does not break a mention.

### 4.4 Command line

Mirrors ghwatch:

```sh
slack-tabbed-tui                       # open the TUI
slack-tabbed-tui add <thread URL>      # watch a thread
slack-tabbed-tui rm <thread>           # stop watching it
slack-tabbed-tui ls                    # list watched threads
slack-tabbed-tui show <thread>         # print a thread, fetched now
slack-tabbed-tui reply <thread> [-]    # post a reply from args or stdin
slack-tabbed-tui status [-json]        # one-line summary (tmux)
slack-tabbed-tui auth <workspace URL>  # log in / check the session (see §6)
slack-tabbed-tui -serve                # run the daemon in the foreground
```

## 5. Feasibility

**Verdict: feasible.** Sign-in is a plain **SSO/browser login**
(decided, see §5.4): no Slack app, nothing published, no admin approval.
The tool uses the same session and endpoints as the Slack web client, so
it sees and can do what the web client does in a thread, including read
state, following a thread and typing indicators. The real risk is that
these endpoints are undocumented and Slack can change them (R1).

### 5.1 How it talks to Slack

All calls go to the Web API methods the Slack web client itself uses,
authenticated with the browser session (`xoxc-` token + `d` cookie, §6).
Most are also documented in Slack's public API reference; the ones that
are not are marked *undocumented* and are verified in M0.

| Need | Method | Notes |
| --- | --- | --- |
| Act as the user | Browser session (`xoxc-` + `d` cookie) | Messages are posted as the user. Reads what the user can read. |
| Live updates | The web client's WebSocket (`rtm.connect`): `message` (incl. `message_changed` / `message_deleted`), `reaction_added/removed`, `user_typing`, `user_change`, `team_join`, `emoji_changed`, `subteam_*` | Outbound WebSocket only: no public URL, fits a local daemon. The daemon holds one connection per workspace. |
| Fallback when no event arrives | Poll `conversations.replies` with `oldest` | Events only arrive for channels the user is a member of; a thread in a public channel the user has not joined is polled. Also used to resync after a reconnect. |
| Read a thread | `conversations.replies` (+ `conversations.info` for the channel) | |
| Post / reply | `chat.postMessage` with `thread_ts`, `reply_broadcast` | Supports `mrkdwn` and `blocks`. |
| Edit / delete | `chat.update`, `chat.delete` | Own messages only, same as Slack (admins aside). |
| Reactions | `reactions.add/remove` | |
| `@` people and bots | `users.list` (cached), `users.info` for misses, `conversations.members` for ranking | `users.list` returns bots and app users (`is_bot`, `is_app_user`). |
| `@` groups | `usergroups.list` | |
| `#` channels | `users.conversations` / `conversations.list` (cached) | |
| `:` emoji | `emoji.list` + bundled standard set | |
| Files | download via `url_private` with the session; upload via `files.getUploadURLExternal` + `files.completeUploadExternal` | `files.upload` was retired on 2025‑11‑12, so only the new flow. |
| Read state, follow | The web client's thread-mark and thread-subscription endpoints | *Undocumented.* |
| Permalinks | `chat.getPermalink` | For `y`/`Y` on new messages. |
| Who am I / workspace URL | `auth.test`, `team.info` | Maps a link's host to a session. |

**Parsing links.** All forms a user can copy are supported:
`https://<ws>.slack.com/archives/<C>/p<16 digits>` (the `p` timestamp is
`ts` without the dot), with optional `?thread_ts=…&cid=…` when the link is
to a reply (then the thread is `thread_ts`), Enterprise Grid hosts
(`<org>.enterprise.slack.com`), and `https://app.slack.com/client/<T>/<C>/thread/<C>-<ts>`.
A link to a reply adds its thread; a link to a message with no replies yet
adds it as a (new) thread.

**Item ID**, ghwatch style: `thread:<team>/<channel>/<thread_ts>`.

### 5.2 Rate limits

Slack's per-method rate limits (tiers) apply to the session as they do
to the web client; the exact budget is not published for it, so M0
measures it. The design keeps API use low regardless:

- Steady state: events carry new messages, so near 0 calls with 20
  watched threads. A resync is 1 call per thread (more for >1000 replies).
- `users.list` is paginated (≤200 per page): a 50k-user Enterprise Grid
  takes minutes the first time, so the user directory is cached on disk
  and kept up to date with `user_change` / `team_join` events; the TUI
  works before the first sync ends (misses fall back to `users.info`).
- Backoff on `ratelimited`, honoring `Retry-After`.

### 5.3 Feature parity matrix

✅ full · 🟡 partial / degraded / to verify in M0 · ❌ not possible

| Slack feature in a thread | | How / why not |
| --- | --- | --- |
| Read parent + all replies, live | ✅ | WebSocket events + `conversations.replies` |
| `@` users, **bots, apps**, groups, `@here/@channel` | ✅ | §4.3 |
| `#` channel links, `:emoji:` completion incl. custom | ✅ | |
| Rich text: bold/italic/strike/code/quote/lists/links | ✅ | Render `rich_text` blocks; compose in Slack markup |
| Reply, "also send to channel" | ✅ | `reply_broadcast` |
| Edit / delete own messages | ✅ | |
| Reactions add/remove, who reacted | ✅ | names on the selected message |
| Unicode emoji and custom emoji display | 🟡 | Unicode shown; custom emoji shown as `:name:` (images optional, see next row) |
| Images / file previews | 🟡 | Name, size, type; open with `O`. Inline images only on terminals with kitty/iTerm2/sixel graphics (P2, **Q6**) |
| Upload files | ✅ | |
| Bot messages with Block Kit (sections, fields, context, attachments) | 🟡 | Rendered as text with layout approximated |
| **Click** buttons/menus in an app's message | 🟡 | The web client sends these through an undocumented endpoint; P2, to verify in M0. Until then `O` opens the message in Slack |
| Mention autocomplete ranking identical to Slack | 🟡 | Same signals we can see (participants, channel members); Slack's own ranking model is private |
| Unfurls (link previews) | 🟡 | Shown when Slack has attached them to the message |
| Message edited / deleted live | ✅ | `message_changed` / `message_deleted` subtypes |
| Typing indicators ("X is typing") | ✅ | `user_typing` events on the WebSocket, and we can send ours |
| Presence dots | 🟡 | Presence subscription on the WebSocket, as the web client does; to verify in M0 |
| **Read/unread state synced with Slack** | ✅ | Thread-mark endpoint (undocumented): reading here clears Slack's badge and vice versa |
| Follow / unfollow thread in Slack | ✅ | Thread-subscription endpoints (undocumented) |
| Slash commands (`/remind`, `/giphy`, app commands) | 🟡 | The web client runs them through an undocumented endpoint; P2, to verify in M0 |
| Save for later / reminders on a message | 🟡 | Undocumented web-client endpoints; to verify in M0 (**Q7**) |
| Schedule a reply | 🟡 | `chat.scheduleMessage` (P2) |
| Pin message | ✅ | `pins.add` (P2) |
| Huddles in thread, canvases, workflows, Slack AI | ❌ | Out of scope |
| Shared (Slack Connect) channels | ✅ | Users from other orgs resolved with `users.info` |
| DMs / group DM threads | ✅ | |

### 5.4 Sign-in: browser session

The tool signs in as you through a normal Slack login in a browser
window (SSO, Google, email code, password: whatever your workspace uses)
and keeps the resulting session token (`xoxc-` + `d` cookie), the same one
the Slack web client uses. wee-slack, slackdump and other clients work
this way.

- **No Slack app, nothing published, no admin approval.**
- **Not supported by Slack:** the endpoints are the web client's, and
  some workspaces' terms forbid third-party clients. The owner accepts
  this (decision of 2026‑10‑07).
- **Can break** when Slack changes its web client; session expiry or
  device-trust policies log you out (`logged out` state, re-login with
  `auth`).
- **Security:** the session is a full credential (anything you can do in
  the browser), so it is stored in the OS keyring and never logged.

A Slack app with its own tokens (including "Sign in with Slack" / OAuth,
which also needs an app) is **out of scope**.

If session login is blocked by policy in the target workspace, the
project is a no-go there: M0 checks this first.

## 6. Authentication and setup

### Sign-in (once per workspace)

1. `slack-tabbed-tui auth <workspace URL>` opens a browser window (a
   dedicated profile, through the Chrome DevTools protocol, as slackdump
   does).
2. You log in as usual, SSO and 2FA included.
3. The tool reads the `xoxc-` token and the `d` cookie from that session,
   checks them with `auth.test`, stores them and closes the window.
4. When Slack ends the session (policy-dependent, often days to weeks),
   the TUI shows `logged out` and `auth` again restores it.

A manual fallback (copy token and cookie from the browser's dev tools) is
documented for machines where the tool can't drive a browser (**Q11**).

### Storage

Credentials go to the OS keyring (Secret Service), or a `0600` file when
there is none. `$SLACK_TOKEN` and `$SLACK_COOKIE` override, like
ghwatch's `$GITHUB_TOKEN`. One session per workspace; a link's host or
team ID picks it.

## 7. Architecture

Same shape as ghwatch, so most of its design carries over:

```
 Slack WebSocket (web client) ──┐
 Slack Web API (HTTPS) ─────────┴─▶ daemon ──▶ desktop / exec notifications
                                      │
                 snapshot.json ◀──────┤  directory cache (users, groups,
                      │               │  channels, emoji) on disk
          status (tmux)        Unix socket, NDJSON ──▶ TUI, TUI, …
```

- **Daemon**: one per user, auto-started by the TUI, exits 10 s after the
  last client (or `-serve` forever), single-instance lock. Holds the
  WebSocket per workspace, routes events for watched threads,
  polls the rest, keeps the directory cache, sends notifications, writes
  the snapshot.
- **TUI**: renders the snapshot, sends commands (add, unwatch, post,
  edit, delete, react, upload, mark read) over the socket. Shows the last
  snapshot at once, without the network.
- **Snapshot**: versioned public contract like ghwatch's
  `docs/snapshot.md`, so other tools can read it. Message bodies stay in a
  separate per-thread cache file, not in the status snapshot.
- **Files**: `$XDG_CONFIG_HOME/slack-tabbed-tui/watch` (one link per line,
  editable), `config.toml`, `$XDG_STATE_HOME/…/snapshot.json`,
  `…/cache/`, `$XDG_RUNTIME_DIR/…/sock`.
- **Language**: Go, like ghwatch. ghwatch is standard-library only; here
  the WebSocket client is the one missing piece (**Q4**).
- **Reuse ghwatch** code for the terminal layer, rendering style, daemon,
  IPC, notifier and config parser (**Q8**: copy, shared module, or a new
  item kind inside ghwatch?).

### Notifications

Event types (enabled per thread with `n`, chosen globally with `N`, as in
ghwatch): **new reply**, **reply that mentions me** (or a group I am in, or
`@here/@channel`), **reply from someone I picked**, **my message got a
reaction**, **thread edited/deleted**. Default when `n` is turned on: new
reply + mention. Clicking a notification focuses the thread in the TUI if
one is open, else opens it in Slack. Backends: `desktop` (notify-send),
`exec` (JSON on stdin), `none`.

tmux: `#(slack-tabbed-tui status)` → e.g. `💬 ●4 @1`; trailing `!` when
stale.

## 8. Requirements

### Functional

| ID | Requirement | Pri |
| --- | --- | --- |
| F-W1 | Add a thread by any permalink form (§5.1); reject non-thread links with a clear message | P0 |
| F-W2 | Remove a thread; watchlist file edits are picked up live | P0 |
| F-W3 | Optional alias per tab (`a` prompt or `# alias` in the watch file) | P2 |
| F-R1 | Show parent and all replies, paginated, with author, time, edited marker | P0 |
| F-R2 | Render `rich_text`, mrkdwn fallback, mentions as names, channel links, emoji | P0 |
| F-R3 | Live new / edited / deleted messages and reactions within 5 s (WebSocket) or 60 s (polling) | P0 |
| F-R4 | Local unread tracking per thread, "new" divider, unread counts in tabs and title | P0 |
| F-R5 | Show bot/app messages, Block Kit approximated, attachments listed | P1 |
| F-C1 | Compose and send replies; multi-line; drafts kept | P0 |
| F-C2 | `@` completion incl. bots, apps, groups, special mentions, ranked (§4.3) | P0 |
| F-C3 | `#` and `:` completion | P1 |
| F-C4 | "Also send to channel" | P1 |
| F-C5 | Edit / delete own messages | P1 |
| F-C6 | Add / remove reactions with emoji picker | P1 |
| F-C7 | Upload a file to the thread; open / save attachments | P1 |
| F-C8 | Compose in `$EDITOR` | P1 |
| F-N1 | Per-thread notifications, event types, global mute (§7) | P0 |
| F-N2 | tmux status line | P1 |
| F-M1 | Multiple workspaces at once | P1 |
| F-X1 | Open in Slack, copy links | P0 |
| F-X2 | CLI subcommands (§4.4) | P1 |

### Non-functional

- Starts and shows cached content in < 200 ms.
- Steady-state Web API use near zero with the WebSocket; never more than
  50 % of any method's budget; exponential backoff, honor `Retry-After`.
- Works over a lost connection: shows stale state, reconnects, resyncs
  missed messages with `conversations.replies` (no gaps).
- Tokens never logged, never in the snapshot; files `0600`.
- 80×24 terminal minimum; correct width for wide emoji and CJK.
- Tests run without network, like ghwatch's scripted source; a demo daemon
  with a fake Slack for the VHS recording.

## 9. Milestones

| | Scope | Exit criteria |
| --- | --- | --- |
| **M0 spike** (1 wk) | Browser SSO login and session capture, WebSocket client, read one thread live, `users.list` with bots, thread-mark and subscription endpoints, measure rate limits | Works in the target workspace (incl. an Enterprise Grid one with SSO); session lifetime known |
| **M1 read-only** | Watchlist, tabs, rendering, live updates, unread synced with Slack, notifications, tmux, CLI `add/rm/ls/show/status` | Daily use as a "watcher" |
| **M2 reply** | Composer, `@` / `#` / `:` completion, drafts, `$EDITOR`, also-send-to-channel | Can work a thread without opening Slack |
| **M3 parity** | Reactions, edit/delete, files, Block Kit rendering, multi-workspace | Matrix rows marked ✅ all done |
| **M4 polish** | Inline images (graphics protocols), schedule, pins, search, slash commands and app buttons (if M0 confirms) | — |

## 10. Risks

| # | Risk | Impact | Mitigation |
| --- | --- | --- | --- |
| R1 | The browser session stops working (web client change, session policy, terms) | Users logged out, or features break | Keep the client layer small and isolated; clear `logged out` state; notify on expiry (**Q12**); M0 checks policies up front |
| R2 | Slack tightens rate limits for web-client sessions | Polling-heavy design breaks | WebSocket first, polling only as fallback; cache everything |
| R3 | WebSocket events miss some message kinds (e.g. channels not joined, Slack Connect) | Delayed updates | Detect per thread and poll; show `polling` on the tab |
| R4 | "Parity" expectations exceed what we can reach (huddles, canvases, Block Kit fidelity) | Disappointment | Matrix in §5.3 agreed up front; `o` to jump to Slack for the rest |
| R5 | Enterprise policies: session lifetime, IP allowlists, device trust | Auth breaks often | Clear `auth` error states; one-command re-login |
| R6 | Huge directories (100k users) | Slow first sync, memory | Disk cache, incremental updates, lazy `users.info` |
| R7 | Terminal emoji width differences | Broken layout | Width table + `safe` icon set, as in ghwatch |

## 11. Open questions

### Resolved

| # | Question | Decision |
| --- | --- | --- |
| Q2 | How to sign in? | **SSO/browser session only** (2026‑10‑07). A Slack app with its tokens is out of scope |
| Q1 | One shared distributed app, or each user creates their own? | Out of scope: there is no Slack app |
| Q3 | Unread state can't sync with Slack; local-only? | Moot: the browser session syncs read state with Slack |
| Q5 | Token paste vs. guided OAuth? | Out of scope: OAuth needs a Slack app. Sign-in is the browser login (§6) |
| Q9 | Name of the binary? | `slack-tabbed-tui` |

### Open

| # | Question | Proposal |
| --- | --- | --- |
| Q4 | WebSocket client for the web-client event stream: write a small RFC 6455 client (standard library only, like ghwatch) or use a module (`coder/websocket`, or `slack-go/slack` with a cookie-aware HTTP client)? | Own small client; a module as fallback if M0 shows edge cases |
| Q11 | How to capture the session: drive a dedicated browser profile through the DevTools protocol (as slackdump does), read the cookie from the user's existing browser profile, or manual copy from dev tools only? | DevTools-driven login, manual copy as fallback; never read other browser profiles |
| Q12 | When the session expires: only show `logged out`, or also send a desktop notification so watched threads don't go silent unnoticed? | Both |
| Q6 | Inline images in the terminal: worth it? | P2, behind a setting, kitty + sixel |
| Q7 | Which of "save for later / remind me / schedule" matter? | Schedule only (P2) |
| Q8 | Separate binary, or a `thread` item kind inside ghwatch (its `kind.Kind` is the seam)? | Separate app sharing code: the composer and directory cache don't fit ghwatch's "checks" model |
| Q10 | Should replying be able to *join* a public channel the user isn't in? Slack allows replying only to members | Ask before joining |

## 12. References

- ghwatch: README, `docs/tui.md`, `docs/decisions.md`, `docs/development.md`
- Slack: [Web API rate limits](https://docs.slack.dev/apis/web-api/rate-limits),
  [`conversations.replies`](https://docs.slack.dev/reference/methods/conversations.replies/),
  [files.upload retirement](https://docs.slack.dev/changelog/2024-04-a-better-way-to-upload-files-is-here-to-stay)
- Clients using the browser session: [slackdump](https://github.com/rusq/slackdump), [wee-slack](https://github.com/wee-slack/wee-slack)
