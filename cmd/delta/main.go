package main

import (
	"context"
	"fmt"
	"os"

	"github.com/ferriskleier/delta/internal/cli"
)

func main() {
	if err := cli.Run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, cli.FormatError(err))
		os.Exit(1)
	}
}
