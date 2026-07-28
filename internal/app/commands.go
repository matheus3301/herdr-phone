package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/matheus3301/herdr-phone/internal/config"
	"github.com/mdp/qrterminal/v3"
)

// hasHelp reports whether args request help.
func hasHelp(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			return true
		}
	}
	return false
}

// runStart handles `start [--quick] [--foreground]`.
func runStart(env Environment, args []string) int {
	if hasHelp(args) {
		fmt.Fprint(env.Stdout, usage)
		return exitOK
	}
	flags, err := parseBoolFlags(args, "quick", "foreground")
	if err != nil {
		fmt.Fprintf(env.Stderr, "herdr-phone start: %v\n\n%s", err, usage)
		return exitUsage
	}
	cfg, code, done := loadConfigOrReport(env, "start")
	if done {
		return code
	}
	rt, err := env.runtime()
	if err != nil {
		fmt.Fprintf(env.Stderr, "herdr-phone start: %v\n", err)
		return exitError
	}

	opts := StartOptions{Quick: flags["quick"], Foreground: flags["foreground"]}

	if opts.Foreground {
		ctx, stop := newSignalContext()
		defer stop()
		if err := rt.Serve(ctx, cfg, ServeOptions{Quick: opts.Quick}); err != nil {
			fmt.Fprintf(env.Stderr, "herdr-phone start: %v\n", err)
			return exitError
		}
		return exitOK
	}

	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()
	res, err := rt.Start(ctx, cfg, opts)
	if err != nil {
		fmt.Fprintf(env.Stderr, "herdr-phone start: %v\n", err)
		return exitError
	}
	printStartResult(env, res)
	return exitOK
}

// printStartResult reports a start outcome and ends with the one line an operator
// acts on: the URL to open on the phone.
//
// The open target differs by mode because the app gate differs. In named mode
// Cloudflare Access is the interactive gate, so the bare public URL is enough and
// no pairing secret is involved — the `Pairing:` line is deliberately withheld
// there rather than advertising a link that plays no part in signing in. Quick
// tunnels have no edge identity, so the single-use pairing link is the only way in
// and is printed on its own `Pairing:` line as before.
func printStartResult(env Environment, res StartResult) {
	if res.AlreadyRunning {
		fmt.Fprintf(env.Stdout, "herdr-phone is already running (%s mode).\n", res.Mode)
	} else {
		fmt.Fprintf(env.Stdout, "herdr-phone started in %s mode.\n", res.Mode)
	}
	if res.PublicURL != "" {
		fmt.Fprintf(env.Stdout, "Public URL: %s\n", res.PublicURL)
	}
	if res.Mode == config.ModeQuick && res.PairingURL != "" {
		fmt.Fprintf(env.Stdout, "Pairing:    %s\n", res.PairingURL)
	}
	url, isPairing := openURL(res)
	if url == "" {
		return
	}
	fmt.Fprintf(env.Stdout, "\nOpen on your phone: %s\n", url)
	// Each advisory must be true of the URL just printed. Only a named-mode public
	// URL is reachable on Access alone; a quick tunnel without a pairing link is a
	// dead end until one is issued, and must never claim Access signs anyone in.
	switch {
	case isPairing:
		fmt.Fprintln(env.Stdout, "This pairing link works once; run `herdr-phone setup-link` for a new one.")
	case res.Mode == config.ModeQuick:
		fmt.Fprintln(env.Stdout, "Quick mode needs a pairing link to get in; run `herdr-phone setup-link` for one.")
	default:
		fmt.Fprintln(env.Stdout, "Cloudflare Access signs you in; no pairing link is needed.")
	}
}

// openURL picks the URL to open for a start result and reports whether the chosen
// URL is a pairing link (so the caller can describe it accurately). Quick mode
// uses the pairing URL, named mode the bare public URL. Each falls back to the
// other only if its preferred URL is missing, so an unexpected gap still leaves
// the operator something reachable.
func openURL(res StartResult) (url string, isPairing bool) {
	if res.Mode == config.ModeQuick {
		if res.PairingURL != "" {
			return res.PairingURL, true
		}
		return res.PublicURL, false
	}
	if res.PublicURL != "" {
		return res.PublicURL, false
	}
	return res.PairingURL, res.PairingURL != ""
}

// runToggle handles `toggle`: stop a running daemon, otherwise start one in the
// configured (named by default) mode. It is the keybindable one-tap action, so it
// reuses the same Status/Start/Stop seams and deadlines as the explicit commands.
func runToggle(env Environment) int {
	cfg, code, done := loadConfigOrReport(env, "toggle")
	if done {
		return code
	}
	rt, err := env.runtime()
	if err != nil {
		fmt.Fprintf(env.Stderr, "herdr-phone toggle: %v\n", err)
		return exitError
	}

	statusCtx, cancelStatus := context.WithTimeout(context.Background(), statusTimeout)
	defer cancelStatus()
	st, err := rt.Status(statusCtx, cfg)
	if err != nil {
		fmt.Fprintf(env.Stderr, "herdr-phone toggle: %v\n", err)
		return exitError
	}

	if st.Running {
		ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
		defer cancel()
		res, err := rt.Stop(ctx, cfg)
		if err != nil {
			fmt.Fprintf(env.Stderr, "herdr-phone toggle: %v\n", err)
			return exitError
		}
		if res.WasRunning {
			fmt.Fprintln(env.Stdout, "herdr-phone is now off (stopped).")
		} else {
			fmt.Fprintln(env.Stdout, "herdr-phone is now off (was not running).")
		}
		return exitOK
	}

	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()
	res, err := rt.Start(ctx, cfg, StartOptions{})
	if err != nil {
		fmt.Fprintf(env.Stderr, "herdr-phone toggle: %v\n", err)
		return exitError
	}
	printStartResult(env, res)
	return exitOK
}

// runServe handles the internal foreground `serve` entrypoint.
func runServe(env Environment, args []string) int {
	if hasHelp(args) {
		fmt.Fprint(env.Stdout, usage)
		return exitOK
	}
	flags, err := parseBoolFlags(args, "quick")
	if err != nil {
		fmt.Fprintf(env.Stderr, "herdr-phone serve: %v\n\n%s", err, usage)
		return exitUsage
	}
	cfg, code, done := loadConfigOrReport(env, "serve")
	if done {
		return code
	}
	rt, err := env.runtime()
	if err != nil {
		fmt.Fprintf(env.Stderr, "herdr-phone serve: %v\n", err)
		return exitError
	}
	ctx, stop := newSignalContext()
	defer stop()
	if err := rt.Serve(ctx, cfg, ServeOptions{Quick: flags["quick"]}); err != nil {
		fmt.Fprintf(env.Stderr, "herdr-phone serve: %v\n", err)
		return exitError
	}
	return exitOK
}

// runStop handles `stop`.
func runStop(env Environment) int {
	cfg, code, done := loadConfigOrReport(env, "stop")
	if done {
		return code
	}
	rt, err := env.runtime()
	if err != nil {
		fmt.Fprintf(env.Stderr, "herdr-phone stop: %v\n", err)
		return exitError
	}
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	res, err := rt.Stop(ctx, cfg)
	if err != nil {
		fmt.Fprintf(env.Stderr, "herdr-phone stop: %v\n", err)
		return exitError
	}
	if res.WasRunning {
		fmt.Fprintln(env.Stdout, "herdr-phone stopped.")
	} else {
		fmt.Fprintln(env.Stdout, "herdr-phone was not running.")
	}
	return exitOK
}

// runStatus handles `status [--json]`.
func runStatus(env Environment, args []string) int {
	if hasHelp(args) {
		fmt.Fprint(env.Stdout, usage)
		return exitOK
	}
	flags, err := parseBoolFlags(args, "json")
	if err != nil {
		fmt.Fprintf(env.Stderr, "herdr-phone status: %v\n\n%s", err, usage)
		return exitUsage
	}
	cfg, code, done := loadConfigOrReport(env, "status")
	if done {
		return code
	}
	rt, err := env.runtime()
	if err != nil {
		fmt.Fprintf(env.Stderr, "herdr-phone status: %v\n", err)
		return exitError
	}
	ctx, cancel := context.WithTimeout(context.Background(), statusTimeout)
	defer cancel()
	st, err := rt.Status(ctx, cfg)
	if err != nil {
		fmt.Fprintf(env.Stderr, "herdr-phone status: %v\n", err)
		return exitError
	}
	if flags["json"] {
		return printStatusJSON(env, st)
	}
	printStatusText(env, st)
	return exitOK
}

func printStatusJSON(env Environment, st Status) int {
	enc := json.NewEncoder(env.Stdout)
	enc.SetIndent("", "  ")
	payload := map[string]any{
		"running":           st.Running,
		"mode":              st.Mode,
		"public_url":        st.PublicURL,
		"local_address":     st.LocalAddress,
		"version":           st.Version,
		"protocol":          st.Protocol,
		"pid":               st.PID,
		"health":            st.Health,
		"herdr_healthy":     st.HerdrHealthy,
		"tunnel_healthy":    st.TunnelHealthy,
		"state_healthy":     st.StateHealthy,
		"connected_clients": st.ConnectedClients,
	}
	if !st.StartedAt.IsZero() {
		payload["started_at"] = st.StartedAt.UTC().Format(time.RFC3339)
	}
	if err := enc.Encode(payload); err != nil {
		fmt.Fprintf(env.Stderr, "herdr-phone status: %v\n", err)
		return exitError
	}
	return exitOK
}

func printStatusText(env Environment, st Status) {
	if !st.Running {
		fmt.Fprintln(env.Stdout, "herdr-phone: not running")
		return
	}
	fmt.Fprintf(env.Stdout, "herdr-phone: running (%s)\n", st.Health)
	fmt.Fprintf(env.Stdout, "  mode:              %s\n", st.Mode)
	if st.PublicURL != "" {
		fmt.Fprintf(env.Stdout, "  public URL:        %s\n", st.PublicURL)
	}
	if st.LocalAddress != "" {
		fmt.Fprintf(env.Stdout, "  local address:     %s\n", st.LocalAddress)
	}
	if st.Version != "" {
		fmt.Fprintf(env.Stdout, "  version:           %s\n", st.Version)
	}
	if st.PID != 0 {
		fmt.Fprintf(env.Stdout, "  pid:               %d\n", st.PID)
	}
	if !st.StartedAt.IsZero() {
		fmt.Fprintf(env.Stdout, "  started:           %s\n", st.StartedAt.UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(env.Stdout, "  herdr:             %s\n", healthy(st.HerdrHealthy))
	fmt.Fprintf(env.Stdout, "  tunnel:            %s\n", healthy(st.TunnelHealthy))
	fmt.Fprintf(env.Stdout, "  state engine:      %s\n", healthy(st.StateHealthy))
	fmt.Fprintf(env.Stdout, "  connected clients: %d\n", st.ConnectedClients)
}

func healthy(ok bool) string {
	if ok {
		return "healthy"
	}
	return "unhealthy"
}

// runSetupLink handles `setup-link`: rotate the pairing secret and print the URL
// with a best-effort terminal QR code.
func runSetupLink(env Environment) int {
	cfg, code, done := loadConfigOrReport(env, "setup-link")
	if done {
		return code
	}
	rt, err := env.runtime()
	if err != nil {
		fmt.Fprintf(env.Stderr, "herdr-phone setup-link: %v\n", err)
		return exitError
	}
	ctx, cancel := context.WithTimeout(context.Background(), statusTimeout)
	defer cancel()
	link, err := rt.RotatePairing(ctx, cfg)
	if err != nil {
		fmt.Fprintf(env.Stderr, "herdr-phone setup-link: %v\n", err)
		return exitError
	}
	fmt.Fprintln(env.Stdout, "Scan this QR code or open the link on your phone (single use):")
	fmt.Fprintln(env.Stdout)
	// Best-effort terminal QR. Half-block rendering keeps it compact on a phone
	// hotspot terminal; the URL below always works if the QR does not render.
	qrterminal.GenerateHalfBlock(link.URL, qrterminal.L, env.Stdout)
	fmt.Fprintln(env.Stdout)
	fmt.Fprintln(env.Stdout, link.URL)
	return exitOK
}

// Command deadlines. start is generous because it waits for the front door to be
// truly ready — cloudflared's edge connection (named) or a confirmed public
// probe (quick) — before printing a pairing link.
const (
	startTimeout  = 150 * time.Second
	stopTimeout   = 30 * time.Second
	statusTimeout = 10 * time.Second
)

// loadConfigOrReport loads configuration, printing an error and returning a
// completion signal when it fails. On success done is false and the config is
// returned.
func loadConfigOrReport(env Environment, cmd string) (cfg config.Config, code int, done bool) {
	c, err := env.loadConfig()
	if err != nil {
		fmt.Fprintf(env.Stderr, "herdr-phone %s: %v\n", cmd, err)
		return config.Config{}, exitError, true
	}
	return c, exitOK, false
}
