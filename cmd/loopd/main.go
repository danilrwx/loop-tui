package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/danilrwx/loop-tui/internal/notify"
	"github.com/danilrwx/loop-tui/internal/session"
)

var version = "dev"

func main() {
	serverFlag := flag.String("server", "", "Loop server URL (defaults to LOOP_SERVER or existing SSO configuration)")
	showVersion := flag.Bool("version", false, "print daemon version")

	flag.Parse()

	if *showVersion {
		fmt.Println("loopd", version)
		return
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()

	run := func() error {
		server, err := session.Server(*serverFlag)
		if err != nil {
			return err
		}

		token, err := session.Token(ctx, server, *serverFlag != "")
		if err != nil {
			return err
		}

		if token == "" {
			return fmt.Errorf("set LOOP_TOKEN or configure browser SSO for loopd")
		}

		return notify.Run(ctx, server, token)
	}
	if err := run(); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
