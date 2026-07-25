//go:build !unix

package config

import "errors"

// errUnsupportedPlatform is returned by the filesystem verification helpers on
// non-unix platforms. herdr-phone targets macOS for v0.2.0; these guards keep
// the package cross-compilable without pretending to enforce unix permissions.
var errUnsupportedPlatform = errors.New("config: filesystem verification is only supported on unix hosts")

// VerifySecretFile is unsupported on non-unix platforms.
func VerifySecretFile(string) error { return errUnsupportedPlatform }

// VerifyWorkspaceRoots is unsupported on non-unix platforms.
func VerifyWorkspaceRoots([]string) ([]string, error) { return nil, errUnsupportedPlatform }
