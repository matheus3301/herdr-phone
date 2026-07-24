package app

import (
	"context"
	"fmt"
	"time"

	"github.com/matheus3301/herdr-phone/internal/config"
)

const doctorTimeout = 30 * time.Second

// runDoctor validates configuration and credential-file permissions (which this
// package owns and can check deterministically), then delegates live
// environment diagnostics — Herdr reachability, cloudflared, tunnel credentials,
// and state health — to the orchestration backend. It never prints secret
// values and never reports overall success from configuration alone.
func runDoctor(env Environment) int {
	out := env.Stdout
	ok := true
	report := func(status, msg string) { fmt.Fprintf(out, "%-7s %s\n", status, msg) }
	markFail := func() { ok = false }

	cfg, err := env.loadConfig()
	if err != nil {
		report("[fail]", "Configuration: "+err.Error())
		fmt.Fprintln(out, "\nConfiguration is invalid; fix it before running other checks.")
		return exitError
	}
	source := cfg.SourcePath
	if source == "" {
		source = "built-in defaults (no config file)"
	}
	report("[ok]", "Configuration: valid — "+source)

	if !checkCredentialFiles(cfg, report) {
		markFail()
	}
	if !checkWorkspaceRoots(cfg, report) {
		markFail()
	}

	rt, rerr := env.runtime()
	if rerr != nil {
		report("[fail]", "Environment diagnostics: "+rerr.Error())
		markFail()
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), doctorTimeout)
		defer cancel()
		rep, derr := rt.Doctor(ctx, cfg)
		if derr != nil {
			report("[fail]", "Environment diagnostics: "+derr.Error())
			markFail()
		} else {
			for _, c := range rep.Checks {
				if c.OK {
					report("[ok]", c.Name+": "+c.Detail)
				} else {
					report("[fail]", c.Name+": "+c.Detail)
					markFail()
				}
			}
		}
	}

	if ok {
		fmt.Fprintln(out, "\nAll checks passed.")
		return exitOK
	}
	fmt.Fprintln(out, "\nSome checks failed; see above.")
	return exitError
}

// checkCredentialFiles verifies the configured credential/token files are
// regular, current-user-owned, and not group/other readable.
func checkCredentialFiles(cfg config.Config, report func(string, string)) bool {
	ok := true
	files := []struct {
		label string
		path  string
	}{
		{"cloudflare.credentials_file", cfg.Cloudflare.CredentialsFile},
		{"cloudflare.token_file", cfg.Cloudflare.TokenFile},
	}
	any := false
	for _, f := range files {
		if f.path == "" {
			continue
		}
		any = true
		if err := config.VerifySecretFile(f.path); err != nil {
			report("[fail]", f.label+": "+err.Error())
			ok = false
		} else {
			report("[ok]", f.label+": "+f.path+" (permissions ok)")
		}
	}
	if !any {
		report("[ok]", "Credential files: none configured")
	}
	return ok
}

// checkWorkspaceRoots verifies the configured directory roots exist and resolve.
func checkWorkspaceRoots(cfg config.Config, report func(string, string)) bool {
	if len(cfg.Server.AllowedWorkspaceRoots) == 0 {
		report("[ok]", "Workspace roots: none configured (directory browsing disabled)")
		return true
	}
	resolved, err := config.VerifyWorkspaceRoots(cfg.Server.AllowedWorkspaceRoots)
	if err != nil {
		report("[fail]", "Workspace roots: "+err.Error())
		return false
	}
	for _, r := range resolved {
		report("[ok]", "Workspace root: "+r)
	}
	return true
}
