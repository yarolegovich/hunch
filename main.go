// Command hunch asks fast, typed judgments about each input record.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/yarolegovich/hunch/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.Main(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
