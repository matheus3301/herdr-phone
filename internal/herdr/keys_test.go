package herdr

import "testing"

func TestValidateKey(t *testing.T) {
	t.Parallel()
	valid := []string{"a", "Z", "5", "enter", "ESC", "tab", "backspace", "up", "down",
		"ctrl+c", "ctrl+h", "alt+x", "shift+tab", "ctrl+alt+delete", "f1", "f12", "f24", " "}
	invalid := []string{"", "ctrl+", "+c", "notakey", "ctrl+notakey", "f0", "f25", "ab",
		"ctrl+\x1b", "esc\n", "hyper+c", "\x07"}
	for _, k := range valid {
		if !ValidateKey(k) {
			t.Errorf("ValidateKey(%q) = false, want true", k)
		}
	}
	for _, k := range invalid {
		if ValidateKey(k) {
			t.Errorf("ValidateKey(%q) = true, want false", k)
		}
	}
}

func TestValidateKeys(t *testing.T) {
	t.Parallel()
	if _, ok := ValidateKeys(nil); ok {
		t.Error("empty batch must be invalid")
	}
	if bad, ok := ValidateKeys([]string{"enter", "boguskey"}); ok || bad != "boguskey" {
		t.Errorf("expected bad=boguskey ok=false, got bad=%q ok=%v", bad, ok)
	}
	if _, ok := ValidateKeys([]string{"ctrl+c", "enter"}); !ok {
		t.Error("valid batch rejected")
	}
}

func TestValidAgentName(t *testing.T) {
	t.Parallel()
	valid := []string{"a", "rev", "reviewer_2", "a-b-c", "z9"}
	invalid := []string{"", "1abc", "Abc", "-x", "a b", "toolong_toolong_toolong_toolong_x", "a!"}
	for _, n := range valid {
		if !ValidAgentName(n) {
			t.Errorf("ValidAgentName(%q) = false, want true", n)
		}
	}
	for _, n := range invalid {
		if ValidAgentName(n) {
			t.Errorf("ValidAgentName(%q) = true, want false", n)
		}
	}
}
