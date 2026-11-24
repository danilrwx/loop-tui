# Loop TUI

**Loop and Mattermost in your terminal.** Follow threads, find unread chats, and reply with Vim-style navigation.

## Install

Available for Linux, macOS, and OpenBSD.

```sh
curl -fsSL https://raw.githubusercontent.com/danilrwx/loop-tui/main/scripts/install.sh | sh
~/.local/bin/loop-tui --demo
```

The installer checks the archive’s SHA-256 checksum and installs to `~/.local/bin`. Set `LOOP_INSTALL_DIR` to choose another directory. The demo uses fictional chats offline. [Download a release archive](https://github.com/danilrwx/loop-tui/releases/latest) · [Build from source](CONTRIBUTING.md)

![Loop TUI showing favorites, unread chats, a Markdown table, and an open thread](docs/demo.png)

## Connect to your server

```sh
~/.local/bin/loop-tui --server https://chat.example.org
```

Enter an access token for that server when prompted. You can set `LOOP_SERVER` and pass the token through `LOOP_TOKEN` for noninteractive startup. Your administrator controls whether personal access tokens are available. [Mattermost token documentation](https://docs.mattermost.com/developers/integrate/reference/personal-access-token)

## What you can do

- **Follow conversations.** Switch between favorites, unread chats, followed threads, and mentions.
- **Search and reply.** Find chats by name or username, search messages, and keep drafts across restarts.
- **Work with attachments.** Pick files by path, paste screenshots, and preview images in Kitty or Sixel terminals.

Thread and activity features depend on the server’s API. Desktop notifications use `notify-send` where available.

[Keyboard shortcuts and setup](docs/usage.md) · [Apache 2.0 license](LICENSE)

Loop TUI shares terminal components with [Telega](https://github.com/danilrwx/telega).
