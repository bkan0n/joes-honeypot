// Package bot implements Joe's Honeypot's Discord behavior.
package bot

import (
	"math/rand"
	"strings"
)

// lookalikes maps ASCII letters to visually confusable Unicode runes
// (Cyrillic/Greek homoglyphs), ported from RiskyMH/honeypot's
// lookalike-chars.yaml. Used to obfuscate the channel name so spam bots
// that blacklist the literal word "honeypot" don't skip the channel.
var lookalikes = map[rune][]rune{
	'a': {'а', 'α'}, // U+0430, U+03B1
	'c': {'с'},      // U+0441
	'e': {'е', 'ε'}, // U+0435, U+03B5
	'h': {'һ'},      // U+04BB
	'i': {'і'},      // U+0456
	'n': {'ո'},      // U+0578
	'o': {'о', 'ο'}, // U+043E, U+03BF
	'p': {'р'},      // U+0440
	's': {'ѕ'},      // U+0455
	't': {'т'},      // U+0442
	'x': {'х'},      // U+0445
	'y': {'у'},      // U+0443
}

var normalizeMap = func() map[rune]rune {
	m := make(map[rune]rune)
	for ascii, subs := range lookalikes {
		for _, r := range subs {
			m[r] = ascii
		}
	}
	return m
}()

// Normalize lowercases s and maps known lookalike runes back to ASCII.
func Normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if ascii, ok := normalizeMap[r]; ok {
			r = ascii
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Obfuscate replaces ~30% of mappable runes in s with random lookalikes,
// guaranteeing at least one replacement.
func Obfuscate(s string, rng *rand.Rand) string {
	runes := []rune(strings.ToLower(s))
	replaced := false
	for i, r := range runes {
		subs, ok := lookalikes[r]
		if !ok {
			continue
		}
		if rng.Float64() < 0.3 {
			runes[i] = subs[rng.Intn(len(subs))]
			replaced = true
		}
	}
	if !replaced {
		for i, r := range runes { // force one deterministic replacement
			if subs, ok := lookalikes[r]; ok {
				runes[i] = subs[rng.Intn(len(subs))]
				break
			}
		}
	}
	return string(runes)
}
