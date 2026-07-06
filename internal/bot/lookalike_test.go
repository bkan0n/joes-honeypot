package bot

import (
	"math/rand"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"honeypot":  "honeypot",
		"hоneypоt":  "honeypot", // Cyrillic о (U+043E)
		"һοnеурοt":  "honeypot", // mixed Cyrillic/Greek lookalikes
		"HONEYPOT":  "honeypot",
		"general":   "general",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestObfuscateRoundTrips(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for range 50 {
		obf := Obfuscate("honeypot", rng)
		if obf == "honeypot" {
			t.Fatal("Obfuscate returned the literal string (no replacements)")
		}
		if got := Normalize(obf); got != "honeypot" {
			t.Fatalf("Normalize(Obfuscate) = %q, want honeypot (obf=%q)", got, obf)
		}
	}
}
