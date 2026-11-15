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
