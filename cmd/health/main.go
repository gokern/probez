// Health is a lightweight binary for Docker healthchecks.
// It exits 0 if the probe endpoint returns 200, 1 otherwise.
//
// Usage:
//
//	HEALTH_PORT=8002 /health
//	HEALTH_PORT=8002 HEALTH_ENDPOINT=/livez /health
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"
)

const requestTimeout = 3 * time.Second

var (
	errPortNotSet       = errors.New("HEALTH_PORT is not set")
	errUnexpectedStatus = errors.New("unexpected status")
)

func main() {
	err := run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		os.Exit(1)
	}
}

//nolint:gosec // URL built from local env vars, not user input
func run() error {
	port := os.Getenv("HEALTH_PORT")
	if port == "" {
		return errPortNotSet
	}

	endpoint := os.Getenv("HEALTH_ENDPOINT")
	if endpoint == "" {
		endpoint = "/readyz"
	}

	url := "http://localhost:" + port + endpoint

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %d", errUnexpectedStatus, resp.StatusCode)
	}

	return nil
}
