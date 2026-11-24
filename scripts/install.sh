#!/bin/sh
set -eu

install_loop_tui() {
    case "$(uname -s)" in
        Linux) os=linux ;;
        OpenBSD) os=openbsd ;;
        Darwin) os=darwin ;;
        *) printf '%s\n' 'Loop TUI builds are available for Linux, macOS and OpenBSD.' >&2; exit 1 ;;
    esac

    case "$(uname -m)" in
        x86_64|amd64) arch=amd64 ;;
        aarch64|arm64) arch=arm64 ;;
        armv7*) arch=armv7 ;;
        *) printf '%s\n' 'Loop TUI builds are available for amd64, arm64 and ARMv7.' >&2; exit 1 ;;
    esac

    archive="loop-tui_${os}_${arch}.tar.gz"
    release=https://github.com/danilrwx/loop-tui/releases/latest/download
    work=$(mktemp -d "${TMPDIR:-/tmp}/loop-tui-install.XXXXXXXXXX")
    staged=
    trap 'if [ -n "$staged" ]; then rm -f "$staged"; fi; rm -rf "$work"' 0
    trap 'exit 1' HUP INT TERM

    printf 'Downloading Loop TUI for %s/%s…\n' "$os" "$arch"
    curl -fsSL "$release/$archive" -o "$work/$archive"
    curl -fsSL "$release/SHA256SUMS" -o "$work/SHA256SUMS"

    expected=$(awk -v name="$archive" '$2 == name { print $1 }' "$work/SHA256SUMS")
    case "$os" in
        darwin) actual=$(shasum -a 256 "$work/$archive") ;;
        openbsd) actual=$(sha256 -q "$work/$archive") ;;
        linux) actual=$(sha256sum "$work/$archive") ;;
    esac
    actual=${actual%% *}
    if [ "$actual" != "$expected" ]; then
        printf '%s\n' 'Checksum verification failed. Retry to download the latest release.' >&2
        exit 1
    fi

    tar -xzf "$work/$archive" -C "$work" loop-tui
    bin=${LOOP_INSTALL_DIR:-"$HOME/.local/bin"}
    mkdir -p "$bin"
    if [ -d "$bin/loop-tui" ]; then
        printf '%s is a directory. Move it before installing.\n' "$bin/loop-tui" >&2
        exit 1
    fi
    staged=$(mktemp "$bin/.loop-tui.XXXXXXXXXX")
    install -m 755 "$work/loop-tui" "$staged"
    mv -f "$staged" "$bin/loop-tui"
    staged=
    printf 'Installed %s\nTry the offline demo with %s --demo\n' "$bin/loop-tui" "$bin/loop-tui"
}

install_loop_tui
