package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/whoisclebs/heimdall-lint/internal/cli"
)

// version is set at build time: -ldflags "-X main.version=1.0.0".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(cli.Run(ctx, os.Args[1:], cli.OSEnvironment(version)))
}
