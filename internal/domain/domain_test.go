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
		// An international host and its ACE (punycode) form are one domain.
		{"例え.テスト", "xn--r8jz45g.xn--zckzah", true, true},
		{"xn--r8jz45g.xn--zckzah", "例え.テスト", true, true},
		{"WWW.例え.テスト", "xn--r8jz45g.xn--zckzah", true, true},
		{"例え.テスト.", "例え.テスト", true, true},
		// The label boundary still holds in punycode.
		{"xn--notexample-9d.com", "example.com", true, false},
	}
	for _, tc := range cases {
		if got := IsOwnDomain(tc.domain, tc.own, tc.subdomains); got != tc.want {
			t.Errorf("IsOwnDomain(%q, %q, %v) = %v, want %v", tc.domain, tc.own, tc.subdomains, got, tc.want)
		}
	}
}

func TestCanonicalHostMapsIDNToPunycode(t *testing.T) {
	cases := map[string]string{
		"例え.テスト":                    "xn--r8jz45g.xn--zckzah",
		"XN--R8JZ45G.XN--ZCKZAH":    "xn--r8jz45g.xn--zckzah",
		"Example.COM.":              "example.com",
		"  example.com  ":           "example.com",
		"bücher.example":            "xn--bcher-kva.example",
		"an-invalid-punycode--.tld": "an-invalid-punycode--.tld",
		"":                          "",
	}
	for host, want := range cases {
		if got := CanonicalHost(host); got != want {
			t.Errorf("CanonicalHost(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestIsPublicSuffix(t *testing.T) {
	publicSuffixes := []string{"github.io", "co.jp", "blogspot.com", "com", "s3.amazonaws.com"}
	for _, host := range publicSuffixes {
		if !IsPublicSuffix(host) {
			t.Errorf("IsPublicSuffix(%q) = false, want true (a shared suffix cannot be one organisation)", host)
		}
	}
	notSuffixes := []string{
		"example.com", "example.shop", "www.example.com", "localhost", "",
		// A real site under a hosting suffix is not the suffix itself.
		"mysite.github.io",
	}
	for _, host := range notSuffixes {
		if IsPublicSuffix(host) {
			t.Errorf("IsPublicSuffix(%q) = true, want false", host)
		}
	}
}

func TestSplitCitationURLKeepsIDNInOnePopulation(t *testing.T) {
	unicode := SplitCitationURL("https://例え.テスト/a")
	ascii := SplitCitationURL("https://xn--r8jz45g.xn--zckzah/a")
	if unicode.Domain == nil || ascii.Domain == nil {
		t.Fatalf("both forms must have a host: %+v %+v", unicode, ascii)
	}
	if *unicode.Domain != *ascii.Domain {
		t.Errorf("domains differ: %q vs %q", *unicode.Domain, *ascii.Domain)
	}
	if unicode.DedupeKey != ascii.DedupeKey {
		t.Errorf("the same page must fold to one identity: %q vs %q", unicode.DedupeKey, ascii.DedupeKey)
	}
	if unicode.Malformed || ascii.Malformed {
		t.Error("neither form is malformed")
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

func TestCitationSourceHostHint(t *testing.T) {
	// Gemini's grounding chunks point at an opaque redirect and name the real site
	// beside it. Without the hint every citation would be vertexaisearch.google
	// and no site could ever be "cited".
	details := NormalizeCitationInputs([]CitationInput{{
		URL:        "https://vertexaisearch.cloud.google.com/grounding-api-redirect/AUZIYQ==",
		SourceHost: "go.dev",
	}}, "go.dev", true)
	if len(details) != 1 {
		t.Fatalf("details = %d, want 1", len(details))
	}
	detail := details[0]
	if detail.Domain == nil || *detail.Domain != "go.dev" {
		t.Fatalf("domain = %v, want the hinted host", detail.Domain)
	}
	if !detail.IsOwn {
		t.Error("a hinted own host must count as own")
	}
	if detail.NormalizedURL != detail.RawURL {
		t.Errorf("normalised = %q, want the raw redirect (nothing there to canonicalise)", detail.NormalizedURL)
	}
	if detail.Malformed {
		t.Error("a hint replaces the opaque URL, it does not make it malformed")
	}

	// A display title that is a sentence is not a host, so the hint is ignored and
	// the URL is read as usual.
	details = NormalizeCitationInputs([]CitationInput{{
		URL:        "https://vertexaisearch.cloud.google.com/grounding-api-redirect/AUZIYQ==",
		SourceHost: "Go 1.27 release notes",
	}}, "go.dev", true)
	if details[0].Domain == nil || *details[0].Domain != "vertexaisearch.cloud.google.com" {
		t.Errorf("domain = %v, want the URL host when the hint is not a host", details[0].Domain)
	}

	// The same hint-vs-URL rule folds duplicates by the raw URL.
	details = NormalizeCitationInputs([]CitationInput{
		{URL: "https://r.example/a", SourceHost: "go.dev"},
		{URL: "https://r.example/a", SourceHost: "go.dev"},
	}, "go.dev", true)
	if len(details) != 1 {
		t.Errorf("details = %d, want 1 (the same opaque URL is one citation)", len(details))
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
