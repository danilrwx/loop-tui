package main

import (
	"flag"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/danilrwx/loop-tui/internal/loop"
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

	flag.Parse()

	if *showVersion {
		fmt.Println("loop-tui", appVersion())

		return
	}

	if err := loop.Run(loop.Options{Server: *server, Login: *login, Demo: *demo}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
