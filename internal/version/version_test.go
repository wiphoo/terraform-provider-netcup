package version

import "testing"

func TestStringReturnsInjectedVersion(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })

	Version = "1.2.3"
	if got := String(); got != "1.2.3" {
		t.Fatalf("String() = %q, want %q", got, "1.2.3")
	}
}

func TestStringFallsBackToDevWhenEmpty(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })

	Version = ""
	if got := String(); got != "dev" {
		t.Fatalf("String() = %q, want %q", got, "dev")
	}
}
