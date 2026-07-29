package interpret

import (
	"testing"

	"github.com/matheus3301/herdr-phone/internal/herdr"
)

// The send-key coupling.
//
// A synthesized send key travels to Herdr through `agent.send_keys`, whose typed
// client validates every token against Herdr's own logical-key grammar
// (internal/herdr/keys.go). That grammar is the last gate before bytes reach an
// agent, and it is owned by a different package for a different reason.
//
// So the two have to agree: if the grammar ever stopped accepting a bare digit, the
// relay would keep publishing answerable prompts that always fail at the last hop —
// the feature would look fine and silently never work. Nothing else in the suite
// would catch it, because the mock relay does not implement the key grammar.
func TestEverySynthesizedSendKeyIsAcceptedByHerdr(t *testing.T) {
	// Cover the whole allowlist, not just the ordinals a fixture happens to use.
	for ordinal := 1; ordinal <= 9; ordinal++ {
		opts := toOptions([]numberedOption{{ordinal: ordinal, label: "option"}})
		key := opts[0].SendKey
		if !validSendKey(key) {
			t.Fatalf("ordinal %d produced key %q, which this package rejects", ordinal, key)
		}
		if !herdr.ValidateKey(key) {
			t.Errorf("ordinal %d produced key %q, which Herdr's key grammar rejects; "+
				"an answerable prompt would always fail at the last hop", ordinal, key)
		}
		if bad, ok := herdr.ValidateKeys([]string{key}); !ok {
			t.Errorf("ordinal %d produced key %q rejected by ValidateKeys (bad=%q)", ordinal, key, bad)
		}
	}
}

// Ordinals outside the allowlist must yield no key at all, so they can never reach
// the grammar in the first place.
func TestOrdinalsOutsideTheAllowlistProduceNoKey(t *testing.T) {
	for _, ordinal := range []int{0, 10, 11, 99, -1} {
		opts := toOptions([]numberedOption{{ordinal: ordinal, label: "option"}})
		if got := opts[0].SendKey; got != "" {
			t.Errorf("ordinal %d produced key %q, want empty", ordinal, got)
		}
	}
}
