package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"

	"github.com/danilrwx/loop-tui/internal/loop"
	"github.com/danilrwx/telega/updater"
)

var version = "dev"

func appVersion() string {
	if version != "dev" {
		return version
	}

	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}

	return version
}

func main() {
	server := flag.String("server", "", "Loop or Mattermost server URL (defaults to LOOP_SERVER)")
	login := flag.Bool("login", false, "open the server in your browser")
	demo := flag.Bool("demo", false, "try the interface offline with sample chats")
	showVersion := flag.Bool("version", false, "print the client version")

	update := flag.Bool("update", false, "install the latest client release")
	checkUpdate := flag.Bool("check-update", false, "check for a newer client release")

	flag.Parse()

	if *update || *checkUpdate {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()

		app := updater.App{Name: "loop-tui", Repository: "danilrwx/loop-tui", Version: appVersion()}
		if err := app.RunCLI(ctx, os.Stdout, *update); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		return
	}

	if *showVersion {
		fmt.Println("loop-tui", appVersion())

		return
	}

	if err := loop.Run(loop.Options{Server: *server, Login: *login, Demo: *demo, Version: appVersion()}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
