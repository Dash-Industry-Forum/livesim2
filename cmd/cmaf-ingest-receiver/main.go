package main

import (
	"log/slog"
	"os"

	"github.com/Dash-Industry-Forum/livesim2/cmd/cmaf-ingest-receiver/app"
)

func main() {
	err := app.Run()
	if err != nil {
		slog.Error("Failed to run", "err", err)
		os.Exit(1)
	}
}
