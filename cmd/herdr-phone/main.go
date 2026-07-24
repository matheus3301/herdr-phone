// Command herdr-phone is a Herdr plugin that relays one authenticated operator's
// Herdr session to their phone over a Cloudflare tunnel. This entrypoint is
// deliberately thin: it collects process inputs and hands them to internal/app,
// which owns command dispatch and orchestration.
package main

import (
	"os"

	"github.com/matheus3301/herdr-phone/internal/app"
	"github.com/matheus3301/herdr-phone/internal/integration"
)

func main() {
	os.Exit(app.Main(app.Environment{
		Getenv: os.Getenv,
		Args:   os.Args[1:],
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		// The production orchestration backend composes config, the Herdr client
		// and state engine, Access auth, pairing/sessions, the security
		// middleware and HTTP/WebSocket server, the terminal bridge, cloudflared,
		// and the daemon control socket.
		Runtime: integration.New(integration.Options{Getenv: os.Getenv}),
	}))
}
