package domain

import (
	"testing"
)

func TestSplitCitationURL(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		domain    string
		dedupe    string
		malformed bool
	}{
		{name: "plain", raw: "https://example.com/a", domain: "example.com", dedupe: "https://example.com/a"},
		{name: "uppercase host", raw: "https://EXAMPLE.com/a", domain: "example.com", dedupe: "https://example.com/a"},
		{name: "trailing dot", raw: "https://example.com./a", domain: "example.com", dedupe: "https://example.com/a"},
		{name: "query and fragment ignored for identity", raw: "https://example.com/a?utm_source=chatgpt#x", domain: "example.com", dedupe: "https://example.com/a"},
		{name: "no path", raw: "https://example.com", domain: "example.com", dedupe: "https://example.com/"},
		{name: "mailto", raw: "mailto:info@example.com", malformed: true},
		{name: "relative", raw: "/guide/thcv", malformed: true},
		{name: "no host", raw: "https:///path", malformed: true},
		{name: "empty", raw: "   ", malformed: true},
		{name: "port kept out of the domain", raw: "https://example.com:8443/a", domain: "example.com", dedupe: "https://example.com/a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := SplitCitationURL(tc.raw)
			if info.Malformed != tc.malformed {
				t.Fatalf("malformed = %v, want %v (%+v)", info.Malformed, tc.malformed, info)
			}
			if tc.malformed {
				return
			}
			if info.Domain == nil || *info.Domain != tc.domain {
				t.Fatalf("domain = %v, want %s", info.Domain, tc.domain)
			}
			if info.DedupeKey != tc.dedupe {
				t.Fatalf("dedupe key = %q, want %q", info.DedupeKey, tc.dedupe)
			}
		})
	}
}

func TestIsOwnDomain(t *testing.T) {
	cases := []struct {
		domain     string
		own        string
		subdomains bool
		want       bool
	}{
		{"example.com", "example.com", true, true},
		{"www.example.com", "example.com", true, true},
		{"www.example.com", "example.com", false, false},
		{"notexample.com", "example.com", true, false},
		{"example.com.evil.test", "example.com", true, false},
		{"example.com", "example.com.", true, true},
		{"", "example.com", true, false},
	}
	for _, tc := range cases {
		if got := IsOwnDomain(tc.domain, tc.own, tc.subdomains); got != tc.want {
			t.Errorf("IsOwnDomain(%q, %q, %v) = %v, want %v", tc.domain, tc.own, tc.subdomains, got, tc.want)
		}
	}
}

func TestNormalizeCitationDetailsFoldsDuplicatesAndKeepsMalformed(t *testing.T) {
	raw := []string{
		"https://www.thch-vape.shop/guide/thcv",
		"https://en.wikipedia.org/wiki/Tetrahydrocannabivarin",
		"https://www.thch-vape.shop/guide/thcv?utm_source=chatgpt#section",
		"mailto:info@example.com",
		"",
	}
	details := NormalizeCitationDetails(raw, "thch-vape.shop", true)
	if len(details) != 3 {
		t.Fatalf("citations = %d, want 3 (duplicate folded, empty skipped, malformed kept)", len(details))
	}
	if !details[0].IsOwn {
		t.Errorf("the own citation was not recognised")
	}
	if details[2].Malformed != true || details[2].Domain != nil {
		t.Errorf("the malformed citation was not kept as malformed: %+v", details[2])
	}
	if details[2].NormalizedURL != "" {
		t.Errorf("a malformed citation must not have a normalised URL")
	}
	for index, detail := range details {
		if detail.Position != index+1 {
			t.Errorf("position = %d, want %d", detail.Position, index+1)
		}
	}
}

func TestNormalizeTextAndBrandMentioned(t *testing.T) {
	if got := NormalizeText("  ＴＨＣＨ   Vape "); got != "thch vape" {
		t.Fatalf("NormalizeText = %q, want %q (NFKC + whitespace folding + lower case)", got, "thch vape")
	}
	if !BrandMentioned("これはリキッド通販ショップの用語集です", []string{"リキッド通販ショップ"}) {
		t.Error("a straight substring match should hit")
	}
	if BrandMentioned("関係ありません", []string{"リキッド通販ショップ"}) {
		t.Error("a missing term must not match")
	}
	if BrandMentioned("", []string{"x"}) {
		t.Error("an empty answer cannot mention anything")
	}
}

func TestBrandTermMatchesOnWordBoundaries(t *testing.T) {
	// A short brand is a real brand: "Go" in "Go 1.27.4" is a mention.
	match, ok := MatchBrandTerm("The latest stable release is **Go 1.27.4**. (go.dev)", "Go")
	if !ok || match.Ambiguous {
		t.Fatalf("match = %+v ok=%t, want a certain mention", match, ok)
	}
	if match.Occurrences != 2 {
		t.Errorf("occurrences = %d, want 2 (Go 1.27.4 and go.dev)", match.Occurrences)
	}

	// The same letters inside a longer word cannot be attributed to the brand.
	match, ok = MatchBrandTerm("Google and the algorithm", "Go")
	if !ok {
		t.Fatal("a substring hit should still be recorded")
	}
	if !match.Ambiguous {
		t.Errorf("match = %+v, want ambiguous (only inside longer words)", match)
	}
	if BrandMentioned("Google and the algorithm", []string{"Go"}) {
		t.Error("an ambiguous hit must not count as mentioned")
	}

	// One certain hit is enough, even when the same letters also appear inside a
	// word elsewhere.
	match, ok = MatchBrandTerm("Google announced Go 1.27", "go")
	if !ok || match.Ambiguous || match.Occurrences != 1 {
		t.Errorf("match = %+v ok=%t, want one certain mention", match, ok)
	}

	// Punctuation is a boundary; a trailing dot or markdown is not part of a word.
	if _, ok := MatchBrandTerm("see go.dev/docs", "go.dev"); !ok {
		t.Error("a term with dots is matched as a substring and should hit")
	}
}

func TestClassifyDomain(t *testing.T) {
	competitors := []string{"rival.example"}
	if got := ClassifyDomain("www.example.shop", "example.shop", true, competitors); got != "own" {
		t.Errorf("own: got %q", got)
	}
	if got := ClassifyDomain("rival.example", "example.shop", true, competitors); got != "competitor" {
		t.Errorf("competitor: got %q", got)
	}
	if got := ClassifyDomain("other.test", "example.shop", true, competitors); got != "other" {
		t.Errorf("other: got %q", got)
	}
	if got := ClassifyDomain("", "example.shop", true, competitors); got != "other (malformed url)" {
		t.Errorf("malformed: got %q", got)
	}
}
