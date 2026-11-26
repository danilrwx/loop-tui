#!/bin/sh
set -eu

install_notifications() {
    if [ "$os" != linux ]; then
        return
    fi
    case "$("$client_target" --help 2>&1)" in
        *'-install-notification-service'*) "$client_target" --install-notification-service "$daemon_target" ;;
        *) printf '%s\n' 'This release has no background notification service installer; service installation skipped.' ;;
    esac
}

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

    if tar -tzf "$work/$archive" | awk '$0 == "loop" { client = 1 } $0 == "loopd" { daemon = 1 } END { exit !(client && daemon) }'; then
        names="loop loopd"
        client_name=loop
    else
        # Older published releases keep their original binary name.
        names=loop-tui
        client_name=loop-tui
    fi
    # shellcheck disable=SC2086 # The verified list contains only fixed binary names.
    tar -xzf "$work/$archive" -C "$work" $names
    bin=${LOOP_INSTALL_DIR:-"$HOME/.local/bin"}
    mkdir -p "$bin"
    bin=$(cd "$bin" && pwd -P)
    case "$bin" in
        *[[:cntrl:]]*) printf '%s\n' 'Cannot install a path containing control characters.' >&2; exit 1 ;;
    esac
    binary_dir=$bin
    # Keep an existing SSO launcher and replace the binaries behind it.
    if [ "$bin" = "$HOME/.local/bin" ] && [ -x "$HOME/.local/libexec/$client_name" ] &&
        [ "$(dd if="$bin/$client_name" bs=2 count=1 2>/dev/null)" = '#!' ]; then
        binary_dir=$HOME/.local/libexec
    fi
    for name in $names; do
        target=$binary_dir/$name
        if [ -d "$target" ]; then
            printf '%s is a directory. Move it before installing.\n' "$target" >&2
            exit 1
        fi
        staged=$(mktemp "$binary_dir/.$name.XXXXXXXXXX")
        install -m 755 "$work/$name" "$staged"
        mv -f "$staged" "$target"
        staged=
        printf 'Installed %s\n' "$target"
    done
    client_target=$binary_dir/$client_name
    daemon_target=$binary_dir/loopd
    if [ "$names" = "loop loopd" ] && [ "$binary_dir" != "$bin" ] && [ ! -e "$bin/loopd" ]; then
        ln -s "$daemon_target" "$bin/loopd"
    fi
    printf 'Try the offline demo with %s --demo\n' "$bin/$client_name"
    if [ "$names" = "loop loopd" ]; then
        install_notifications
    else
        printf '%s\n' 'This release has no background notification daemon; service installation skipped.'
    fi
}

install_loop_tui
