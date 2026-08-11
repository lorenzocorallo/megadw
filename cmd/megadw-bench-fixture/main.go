// Command megadw-bench-fixture serves the project-owned deterministic MEGA
// fixture as a separate process so release resource measurements do not count
// the fixture's plaintext buffer or encryption work against the downloader.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lorenzocorallo/megadw/tests/integration"
)

func main() {
	size := flag.Int("size", 256<<20, "fixture plaintext size in bytes")
	delay := flag.Duration("delay", 20*time.Millisecond, "per-range response delay")
	flag.Parse()

	fixture := integration.NewFakeMegaServerWithOptions(integration.FakeMegaServerOptions{
		PayloadSize: *size,
		Delay:       *delay,
	})
	defer fixture.Close()
	// The parent reads one line before starting its timer. The link is
	// project-owned and contains no maintainer credential.
	_, _ = fmt.Fprintf(os.Stdout, "%s\t%s\n", fixture.APIBaseURL(), fixture.FileLink())

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	<-signals
}
