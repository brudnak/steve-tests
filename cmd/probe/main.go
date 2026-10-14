// Command probe reports the configured target's cache mode without mutations.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"

	"github.com/rancher/steve-tests/actions"
	"github.com/rancher/steve-tests/pkg/client"
	"github.com/rancher/steve-tests/pkg/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if !cfg.ExplicitTarget {
		return fmt.Errorf("set STEVE_URL or STEVE_CONFIG to probe a target")
	}
	// Keep stdout machine-readable even if verbose requests were configured.
	cfg.Verbose = false
	cl, err := client.Discover(cfg)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, cfg.WaitTimeoutDuration())
	defer cancel()
	mode, err := actions.DetectCacheMode(ctx, cl, cfg.ExpectedCacheMode)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		URL       string `json:"url"`
		CacheMode string `json:"cacheMode"`
		Schemas   int    `json:"schemas"`
	}{cfg.URL, mode, len(cl.Schemas)})
}
