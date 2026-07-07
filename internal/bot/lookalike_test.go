package bot

import (
	"math/rand"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"honeypot": "honeypot",
		"hоneypоt": "honeypot", // Cyrillic о (U+043E)
		"һοnеурοt": "honeypot", // mixed Cyrillic/Greek lookalikes
		"HONEYPOT": "honeypot",
		"general":  "general",
	}
	for in, want := range cases {
		if got := normalize(in); got != want {
			t.Errorf("normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestObfuscateRoundTrips(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for range 50 {
		obf := obfuscate("honeypot", rng)
		if obf == "honeypot" {
			t.Fatal("obfuscate returned the literal string (no replacements)")
		}
		if got := normalize(obf); got != "honeypot" {
			t.Fatalf("normalize(obfuscate) = %q, want honeypot (obf=%q)", got, obf)
		}
	}
}
