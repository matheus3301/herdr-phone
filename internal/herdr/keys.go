package herdr

import "strings"

// Logical key names Herdr accepts, validated before any bytes are sent. This
// mirrors the CLI key syntax: single printable characters, named keys, function
// keys, and ctrl+/alt+/shift+ modified keys. Validation happens here so a
// browser can never smuggle an arbitrary control string into send-keys.

var namedKeys = map[string]bool{
	"enter": true, "return": true, "tab": true, "esc": true, "escape": true,
	"backspace": true, "delete": true, "del": true, "insert": true,
	"space": true, "up": true, "down": true, "left": true, "right": true,
	"home": true, "end": true, "pageup": true, "pagedown": true,
}

var modifiers = map[string]bool{"ctrl": true, "alt": true, "shift": true, "meta": true, "super": true}

// ValidateKey reports whether a single logical key token is acceptable. It
// accepts a single character, a named key, an fN function key, or a
// modifier-prefixed key such as "ctrl+c" or "shift+tab".
func ValidateKey(key string) bool {
	if key == "" {
		return false
	}
	parts := strings.Split(key, "+")
	// All but the last part must be modifiers; the last is the base key.
	for _, m := range parts[:len(parts)-1] {
		if !modifiers[strings.ToLower(m)] {
			return false
		}
	}
	return validBaseKey(parts[len(parts)-1])
}

func validBaseKey(base string) bool {
	if base == "" {
		return false
	}
	lower := strings.ToLower(base)
	if namedKeys[lower] {
		return true
	}
	if isFunctionKey(lower) {
		return true
	}
	// A single rune (any printable character) is a valid literal key.
	runes := []rune(base)
	if len(runes) == 1 && runes[0] >= 0x20 && runes[0] != 0x7f {
		return true
	}
	return false
}

func isFunctionKey(s string) bool {
	if len(s) < 2 || s[0] != 'f' {
		return false
	}
	n := 0
	for _, r := range s[1:] {
		if r < '0' || r > '9' {
			return false
		}
		n = n*10 + int(r-'0')
	}
	return n >= 1 && n <= 24
}

// ValidateKeys validates a batch, returning the first invalid key found.
func ValidateKeys(keys []string) (string, bool) {
	if len(keys) == 0 {
		return "", false
	}
	for _, k := range keys {
		if !ValidateKey(k) {
			return k, false
		}
	}
	return "", true
}
