package cli

import "testing"

func TestNewLockCmdSubcommands(t *testing.T) {
	cmd := newLockCmd()

	if cmd.Use != "lock" {
		t.Fatalf("Use = %q, want %q", cmd.Use, "lock")
	}

	want := map[string]bool{
		"init":   true,
		"status": true,
		"clean":  true,
		"reset":  true,
	}

	for _, sub := range cmd.Commands() {
		delete(want, sub.Name())
	}

	if len(want) != 0 {
		t.Fatalf("missing subcommands: %v", want)
	}
}

func TestCountStaleKeys(t *testing.T) {
	tracked := []string{"a", "b", "c", "d"}
	current := []string{"a", "c"}

	if got := countStaleKeys(tracked, current); got != 2 {
		t.Fatalf("countStaleKeys() = %d, want %d", got, 2)
	}

	if got := countStaleKeys(nil, current); got != 0 {
		t.Fatalf("countStaleKeys(nil) = %d, want 0", got)
	}
}
