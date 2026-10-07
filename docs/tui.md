# The TUI

Run `slack-tabbed-tui` to open it. The screen follows ghwatch's (and
jira-tabbed-tui's) style, from top to bottom:

- **Title bar:** the app name, how many threads are watched, unread
  replies (`●4`) and mentions of you (`@1`) across all of them, how many
  have alerts, whether notifications are muted, and the state of each
  workspace: `live` (events arrive as they happen), `polling` (the live
  connection is down; threads are fetched every `interval`),
  `logged out` or `not signed in` (run `slack-tabbed-tui auth`).
- **Tabs:** one per thread, in the order they were added. A label is the
  tab's number (its `1`–`9` key), unread replies (`●3`, or `@3` when one
  mentions you, `!` on error), the channel (`#team-infra`, or `@Bob` for
  a DM) and the first words of the parent message, and a bell when
  alerts are on. A thread from a workspace other than the most common
  one gets the workspace name first.
- **Thread header:** channel and workspace, replies and people; then the
  parent message, the time of the last reply, unread count and alerts.
- **Messages:** the parent, a `── N replies ──` rule, then the replies,
  oldest first. Each message shows its author (`APP` for bots, `(you)`
  for your own), time, `(edited)`, the text with mentions as names,
  attachments and link previews (`┃`), files, and reactions (yours
  highlighted). A red `── new ──` rule marks where unread replies start.
  The selected message has a bar on its left.
- **Reply box** (`i`): grows above the footer while you write.
- **Footer:** the main keys and the connection to the daemon. Messages
  replace the hints for a few seconds.

A thread counts as read once it has been on screen for a moment, as in
Slack; `u` marks it read at once. Read state is kept by the daemon, so
it is the same in every open TUI.

## Keys

| Key | Action |
| --- | --- |
| `h` / `l`, `gT` / `gt` | previous / next thread |
| `1`–`9` | jump to thread N |
| `j` / `k`, `gg` / `G` | next / previous message, first / last |
| `ctrl+d` / `ctrl+u` | half a page down / up |
| `/` | find a message by author or text; `enter` selects it |
| `i` | reply in the thread |
| `enter` | open the selected message in Slack |
| `o` | open the thread in Slack |
| `O` | open the selected message's file or first link |
| `y` / `Y` | copy the thread / message link |
| `u` | mark the thread read |
| `n` | toggle notifications for the thread |
| `N` | notification settings: event types and global mute |
| `a` | add a thread (paste a message link) |
| `d` | unwatch the thread (in every client) |
| `r` | fetch every thread now |
| `?` | all keys |
| `q` | quit (the daemon keeps running) |

In the reply box: `enter` sends, `alt+enter` or `ctrl+j` starts a new
line, `ctrl+b` toggles "also send to the channel", `esc` closes the box
and keeps the draft (per thread), `ctrl+c` discards it.

## Trying it without Slack

`docs/demo/fakeslack` serves a fake workspace with sample threads and
someone replying every few seconds:

```sh
go run ./docs/demo/fakeslack -home /tmp/stt-demo &
XDG_CONFIG_HOME=/tmp/stt-demo/config XDG_STATE_HOME=/tmp/stt-demo/state \
XDG_CACHE_HOME=/tmp/stt-demo/cache XDG_RUNTIME_DIR=/tmp/stt-demo/run \
  go run ./cmd/slack-tabbed-tui
```
