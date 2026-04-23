package probez_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gokern/probez"
)

// Minimal lifecycle: start, mark ready, query /readyz.
func Example() {
	err := probez.Start(0,
		probez.WithHost("127.0.0.1"),
		probez.WithStaleAfter(30*time.Second),
		probez.WithReadinessCheck("db", func(_ context.Context) error {
			return nil // simulate healthy DB
		}),
	)
	if err != nil {
		panic(err)
	}

	defer func() { _ = probez.Close(context.Background()) }()

	probez.MarkStarted()
	probez.MarkReady()
	probez.Ping()

	resp, err := http.Get("http://" + probez.Addr() + "/readyz")
	if err != nil {
		panic(err)
	}

	_ = resp.Body.Close()
	fmt.Println(resp.StatusCode)
	// Output: 200
}

// HTTP server with WithAutoLive: responding to /livez is itself proof of life,
// so no Ping() is needed. Fits API services whose request path already
// signals liveness.
func ExampleWithAutoLive() {
	api := &http.Server{
		Addr:              "127.0.0.1:0",
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("ok"))
		}),
	}

	p, err := probez.New(0,
		probez.WithHost("127.0.0.1"),
		probez.WithAutoLive(),
	)
	if err != nil {
		panic(err)
	}

	defer func() { _ = p.Close(context.Background()) }()
	defer func() { _ = api.Shutdown(context.Background()) }()

	// API came up; no background loop to heartbeat from.
	p.MarkStarted()
	p.MarkReady()

	resp, err := http.Get("http://" + p.Addr() + "/livez")
	if err != nil {
		panic(err)
	}

	_ = resp.Body.Close()
	fmt.Println(resp.StatusCode)
	// Output: 200
}

// Background worker: a processing loop calls Ping() every iteration so /livez
// can detect a stuck consumer. If the loop blocks for longer than WithStaleAfter,
// /livez returns 503 and Kubernetes restarts the pod.
func ExampleProbe_Ping() {
	p, err := probez.New(0,
		probez.WithHost("127.0.0.1"),
		probez.WithStaleAfter(2*time.Second),
		probez.WithStartupGrace(0),
	)
	if err != nil {
		panic(err)
	}

	defer func() { _ = p.Close(context.Background()) }()

	p.MarkStarted()
	p.MarkReady()

	queue := make(chan int, 3)
	for msg := range 3 {
		queue <- msg + 1
	}

	close(queue)

	var processed atomic.Int64

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})

	go func() {
		defer close(done)

		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-queue:
				if !ok {
					return
				}

				p.Ping() // heartbeat before each unit of work
				processed.Add(int64(msg))
			}
		}
	}()

	<-done

	resp, err := http.Get("http://" + p.Addr() + "/livez")
	if err != nil {
		panic(err)
	}

	_ = resp.Body.Close()
	fmt.Println("livez:", resp.StatusCode)
	fmt.Println("processed:", processed.Load())
	// Output:
	// livez: 200
	// processed: 6
}

// Readiness check that reports an external dependency's state. On /readyz
// every registered check runs with the request context; any non-nil error
// flips the response to 503.
func ExampleWithReadinessCheck() {
	var dbUp atomic.Bool

	dbUp.Store(false)

	p, err := probez.New(0,
		probez.WithHost("127.0.0.1"),
		probez.WithStaleAfter(time.Hour),
		probez.WithStartupGrace(time.Hour),
		probez.WithReadinessCheck("db", func(_ context.Context) error {
			if !dbUp.Load() {
				return errors.New("db not connected")
			}

			return nil
		}),
	)
	if err != nil {
		panic(err)
	}

	defer func() { _ = p.Close(context.Background()) }()

	p.MarkStarted()
	p.MarkReady()
	p.Ping()

	// DB still down — /readyz fails.
	resp, _ := http.Get("http://" + p.Addr() + "/readyz")
	_ = resp.Body.Close()
	fmt.Println("before:", resp.StatusCode)

	// DB connects — /readyz recovers.
	dbUp.Store(true)

	resp, _ = http.Get("http://" + p.Addr() + "/readyz")
	_ = resp.Body.Close()
	fmt.Println("after:", resp.StatusCode)
	// Output:
	// before: 503
	// after: 200
}
