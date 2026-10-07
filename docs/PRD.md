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
   thread pane you can do here, as far as the public API allows
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
  replies and mentions, mute state, connection state (`live` over Socket
  Mode, `polling`, or yellow `last sync … ago` when failing).
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
| `u` | mark thread read (locally) | — |
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
slack-tabbed-tui auth                  # set up / check tokens (see §6)
slack-tabbed-tui manifest              # print the Slack app manifest
slack-tabbed-tui -serve                # run the daemon in the foreground
```

## 5. Feasibility

**Verdict: feasible**, with a Slack app the user creates in their own
workspace (an "internal, customer-built" app) and a **user token**. Almost
all of the thread pane can be built on public, documented APIs. The real
risks are not technical: getting the app approved by workspace admins
(R1), and a few Slack features that have no public API (table below).

### 5.1 How it talks to Slack

| Need | API | Notes |
| --- | --- | --- |
| Act as the user | User OAuth token (`xoxp-`) of an app installed by the user | Messages are posted as the user, not as a bot. Reads what the user can read. |
| Live updates | **Socket Mode** (app-level token `xapp-`, `connections:write`) + Events API subscribed *on behalf of the user*: `message.channels/groups/im/mpim`, `reaction_added/removed`, `user_change`, `team_join`, `emoji_changed`, `subteam_*` | Outbound WebSocket only: no public URL, fits a local daemon. The daemon holds one connection. |
| Fallback when no event arrives | Poll `conversations.replies` with `oldest` | Events only arrive for channels the user is a member of; a thread in a public channel the user has not joined is polled. Also used to resync after a reconnect. |
| Read a thread | `conversations.replies` (+ `conversations.info` for the channel) | Tier 3 (~50/min) for internal apps. |
| Post / reply | `chat.postMessage` with `thread_ts`, `reply_broadcast` | Supports `mrkdwn` and `blocks`. |
| Edit / delete | `chat.update`, `chat.delete` | Own messages only, same as Slack (admins aside). |
| Reactions | `reactions.add/remove` | |
| `@` people and bots | `users.list` (cached), `users.info` for misses, `conversations.members` for ranking | `users.list` returns bots and app users (`is_bot`, `is_app_user`). |
| `@` groups | `usergroups.list` | Needs `usergroups:read`. |
| `#` channels | `users.conversations` / `conversations.list` (cached) | |
| `:` emoji | `emoji.list` + bundled standard set | |
| Files | download via `url_private` with the token; upload via `files.getUploadURLExternal` + `files.completeUploadExternal` | `files.upload` was retired on 2025‑11‑12, so only the new flow. |
| Permalinks | `chat.getPermalink` | For `y`/`Y` on new messages. |
| Who am I / workspace URL | `auth.test`, `team.info` | Maps a link's host to a token. |

**Parsing links.** All forms a user can copy are supported:
`https://<ws>.slack.com/archives/<C>/p<16 digits>` (the `p` timestamp is
`ts` without the dot), with optional `?thread_ts=…&cid=…` when the link is
to a reply (then the thread is `thread_ts`), Enterprise Grid hosts
(`<org>.enterprise.slack.com`), and `https://app.slack.com/client/<T>/<C>/thread/<C>-<ts>`.
A link to a reply adds its thread; a link to a message with no replies yet
adds it as a (new) thread.

**Item ID**, ghwatch style: `thread:<team>/<channel>/<thread_ts>`.

### 5.2 Rate limits

Since 2025‑05‑29 Slack limits `conversations.history` and
`conversations.replies` to **1 request/minute and 15 messages per call for
apps distributed commercially outside the Marketplace**. Marketplace and
**internal, customer-built apps keep Tier 3**. Each user creating their
own app from our manifest is an internal app, so the limit does not apply.
This is also why we do **not** ship a single shared, distributed "official"
app (**Q1**).

Budget with 20 watched threads: events carry new messages, so steady state
is near 0 calls. A resync is 1 call per thread (more for >1000 replies).
`users.list` is Tier 2 and paginated (≤200 per page): a 50k-user
Enterprise Grid takes minutes the first time, so the user directory is
cached on disk and kept up to date with `user_change` / `team_join`
events; the TUI works before the first sync ends (misses fall back to
`users.info`).

### 5.3 Feature parity matrix

✅ full · 🟡 partial / degraded · ❌ not possible with public APIs

| Slack feature in a thread | | How / why not |
| --- | --- | --- |
| Read parent + all replies, live | ✅ | Socket Mode events + `conversations.replies` |
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
| **Click** buttons/menus in another app's message | ❌ | `block_actions` go only to the app that owns the message. `O` opens the message in Slack instead |
| Mention autocomplete ranking identical to Slack | 🟡 | Same signals we can see (participants, channel members); Slack's own ranking model is private |
| Unfurls (link previews) | 🟡 | Shown when Slack has attached them to the message |
| Message edited / deleted live | ✅ | `message_changed` / `message_deleted` subtypes |
| Typing indicators ("X is typing") | ❌ | Only in the legacy RTM API, which new apps cannot use |
| Presence dots | 🟡 | `users.getPresence` on demand; no live presence for new apps |
| **Read/unread state synced with Slack** | ❌ | No public API marks a thread read. Unread is tracked locally; reading here doesn't clear Slack's badge (**Q3**) |
| Follow / unfollow thread in Slack | ❌ | No public API; we can only *show* whether you're subscribed when Slack tells us (`reply_users`, mentions) |
| Slash commands (`/remind`, `/giphy`, app commands) | ❌ | No public API to invoke them as a user |
| Save for later / reminders on a message | ❌ / 🟡 | "Later" has no API; `reminders.add` exists but is limited (**Q7**) |
| Schedule a reply | 🟡 | `chat.scheduleMessage` (P2) |
| Pin message | ✅ | `pins.add` (P2) |
| Huddles in thread, canvases, workflows, Slack AI | ❌ | Out of scope |
| Shared (Slack Connect) channels | ✅ | Users from other orgs resolved with `users.info` |
| DMs / group DM threads | ✅ | With `im:*` / `mpim:*` scopes |

### 5.4 The alternative we rejected (for now): session tokens

Clients such as wee-slack and slackdump can use the browser session token
(`xoxc-` + `d` cookie). That needs no app and no admin, and unlocks
undocumented endpoints (thread read state, typing, follow). But it
breaks Slack's terms for most workspaces, breaks with SSO/device-trust
policies, can stop working any day, and makes the user's full session
credential sit on disk. Recommendation: **not supported in v1**; revisit
only as an explicit, opt-in "unsupported" mode if R1 blocks most users
(**Q2**).

## 6. Authentication and setup

1. `slack-tabbed-tui manifest` prints an app manifest (YAML) with the
   scopes below, Socket Mode on and the user events subscribed.
2. The user creates the app at api.slack.com/apps → "From manifest",
   installs it to the workspace (admin approval may be needed), and
   generates an app-level token with `connections:write`.
3. `slack-tabbed-tui auth` asks for the user token (`xoxp-`) and the app
   token (`xapp-`), checks them with `auth.test`, and stores them in the
   OS keyring (Secret Service), or in a `0600` file when there is none.
   `$SLACK_USER_TOKEN` / `$SLACK_APP_TOKEN` override, like ghwatch's
   `$GITHUB_TOKEN`.
4. One token pair per workspace; a link's host or team ID picks the pair.

**User scopes** (minimum): `channels:history`, `groups:history`,
`im:history`, `mpim:history`, `channels:read`, `groups:read`, `im:read`,
`mpim:read`, `users:read`, `usergroups:read`, `emoji:read`,
`reactions:read`, `reactions:write`, `chat:write`, `files:read`,
`files:write`, `team:read`, `pins:write` (P2). `users:read.email` is not
needed.

A guided OAuth flow (local browser redirect) instead of pasting tokens is
nice to have (**Q5**).

## 7. Architecture

Same shape as ghwatch, so most of its design carries over:

```
 Slack Socket Mode (WebSocket) ─┐
 Slack Web API (HTTPS) ─────────┴─▶ daemon ──▶ desktop / exec notifications
                                      │
                 snapshot.json ◀──────┤  directory cache (users, groups,
                      │               │  channels, emoji) on disk
          status (tmux)        Unix socket, NDJSON ──▶ TUI, TUI, …
```

- **Daemon**: one per user, auto-started by the TUI, exits 10 s after the
  last client (or `-serve` forever), single-instance lock. Holds the
  Socket Mode connection per workspace, routes events for watched threads,
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
| F-R3 | Live new / edited / deleted messages and reactions within 5 s (Socket Mode) or 60 s (polling) | P0 |
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
- Steady-state Web API use near zero with Socket Mode; never more than
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
| **M0 spike** (1 wk) | Manifest, tokens, Socket Mode client, read one thread live, `users.list` with bots | Resolves R1–R3 in a real workspace (personal + an Enterprise Grid one) |
| **M1 read-only** | Watchlist, tabs, rendering, live updates, local unread, notifications, tmux, CLI `add/rm/ls/show/status` | Daily use as a "watcher" |
| **M2 reply** | Composer, `@` / `#` / `:` completion, drafts, `$EDITOR`, also-send-to-channel | Can work a thread without opening Slack |
| **M3 parity** | Reactions, edit/delete, files, Block Kit rendering, multi-workspace | Matrix rows marked ✅ all done |
| **M4 polish** | Inline images (graphics protocols), schedule, pins, search, OAuth flow | — |

## 10. Risks

| # | Risk | Impact | Mitigation |
| --- | --- | --- | --- |
| R1 | Workspace admins don't allow user-created apps or the scopes we need | Blocking for that user | Minimal scopes, clear manifest; document the admin request; **Q2** |
| R2 | Slack changes limits for internal apps too | Polling-heavy design breaks | Socket Mode first, polling only as fallback; cache everything |
| R3 | User-scoped events in Socket Mode miss some message kinds (e.g. channels not joined, Slack Connect) | Delayed updates | Detect per thread and poll; show `polling` on the tab |
| R4 | "Parity" expectations exceed the API (read state, slash commands, buttons) | Disappointment | Matrix in §5.3 agreed up front; `o` to jump to Slack for the rest |
| R5 | Enterprise policies: token lifetime, IP allowlists, session expiry | Auth breaks often | Token rotation support; clear `auth` error states |
| R6 | Huge directories (100k users) | Slow first sync, memory | Disk cache, incremental updates, lazy `users.info` |
| R7 | Terminal emoji width differences | Broken layout | Width table + `safe` icon set, as in ghwatch |

## 11. Open questions

| # | Question | Proposal |
| --- | --- | --- |
| Q1 | One shared distributed app, or each user creates their own from the manifest? | Own app: no 1 req/min limit, no hosted OAuth, no Marketplace review |
| Q2 | Support `xoxc` session tokens as an opt-in "unsupported" mode for workspaces where apps are blocked? | No in v1; reconsider after M0 |
| Q3 | Unread state can't sync with Slack. Is local-only unread acceptable? | Yes; `u` marks read locally |
| Q4 | Stay standard-library only (write a small RFC 6455 client) or use `slack-go/slack` + its `socketmode`? | Own small client, in line with ghwatch; `slack-go` as fallback if M0 shows edge cases |
| Q5 | Token paste vs. guided OAuth with a local redirect? | Paste in v1, OAuth in M4 |
| Q6 | Inline images in the terminal: worth it? | P2, behind a setting, kitty + sixel |
| Q7 | Which of "save for later / remind me / schedule" matter? | Schedule only (P2) |
| Q8 | Separate binary, or a `thread` item kind inside ghwatch (its `kind.Kind` is the seam)? | Separate app sharing code: the composer and directory cache don't fit ghwatch's "checks" model |
| Q9 | Name of the binary (`slack-tabbed-tui` is long): `slackwatch`? `sthreads`? | — |
| Q10 | Should replying be able to *join* a public channel the user isn't in? Slack allows replying only to members | Ask before joining |

## 12. References

- ghwatch: README, `docs/tui.md`, `docs/decisions.md`, `docs/development.md`
- Slack: [Web API rate limits](https://docs.slack.dev/apis/web-api/rate-limits),
  [rate limit changes for non‑Marketplace apps (2025‑05‑29)](https://docs.slack.dev/changelog/2025/05/29/rate-limit-changes-for-non-marketplace-apps),
  [`conversations.replies`](https://docs.slack.dev/reference/methods/conversations.replies/),
  [files.upload retirement](https://docs.slack.dev/changelog/2024-04-a-better-way-to-upload-files-is-here-to-stay),
  [Events API](https://api.slack.com/events-api)
