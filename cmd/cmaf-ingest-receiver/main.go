package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/Dash-Industry-Forum/livesim2/cmd/cmaf-ingest-receiver/app"
)

func main() {

	opts, err := app.ParseOptions()
	if err != nil {
		flag.Usage()
		slog.Error("failed to parse options", "err", err)
		os.Exit(1)
	}

	err = app.Run(opts)
	if err != nil {
		slog.Error("Failed to run", "err", err)
		os.Exit(1)
	}
}
