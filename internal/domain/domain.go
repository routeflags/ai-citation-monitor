// Package domain holds the measurement semantics that decide what a record
// means: URL normalisation, own-domain matching and brand-mention matching.
//
// Nothing here performs I/O. Changing the rules in this file changes the
// meaning of cited_own / brand_mentioned, so the package exposes
// NormalizerVersion which is written into every record as tool_version.
package domain

import (
	"net/url"
	"strings"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
	"golang.org/x/text/unicode/norm"
)

// NormalizerVersion is the version of the normalisation and matching rules.
// Bump it whenever URL normalisation (query/fragment handling, trailing dot,
// IDN mapping, subdomain matching) or brand matching (NFKC) changes: cited_own and
// brand_mentioned would change meaning, and records must stay comparable only
// within one version.
//
// 2: hosts are compared in punycode, so a Unicode citation and its ACE form are
// one population (1 compared the raw host).
// 3: a provider may name the real site next to an opaque citation URL (Gemini's
// grounding redirect). Without the hint every Gemini citation was Google's
// redirect host, so cited_own could never be true for that provider.
const NormalizerVersion = "3"

// Citation is one cited URL of a run, after normalisation. Position is 1-based
// and counts after duplicate folding. Domain is nil when the URL has no usable
// host (malformed); such citations are kept, never dropped, so citation counts
// stay additive.
type Citation struct {
	Position  int     `json:"position"`
	URL       string  `json:"url"`
	Domain    *string `json:"domain"`
	IsOwn     bool    `json:"is_own"`
	Malformed bool    `json:"malformed"`
}

// URLInfo is the result of splitting a raw citation URL.
type URLInfo struct {
	URL       string
	Domain    *string
	DedupeKey string
	Malformed bool
}

// NormalizeText collapses a string for comparison: NFKC, whitespace folding and
// lower-casing.
func NormalizeText(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToLower(strings.Join(strings.Fields(norm.NFKC.String(value)), " "))
}

// CanonicalHost is the form in which hosts are compared and stored: lower case,
// no trailing dot, and international labels mapped to punycode. "例え.テスト" and
// "xn--r8jz45g.xn--zckzah" therefore become one host, which is what decides
// whether a citation is yours.
//
// A label that cannot be mapped is returned as-is: an odd host is still a citation
// that came back from the provider, and dropping it would change the denominator.
func CanonicalHost(host string) string {
	trimmed := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if trimmed == "" {
		return ""
	}
	ascii, err := idna.Lookup.ToASCII(trimmed)
	if err != nil || ascii == "" {
		return trimmed
	}
	return strings.ToLower(ascii)
}

// IsPublicSuffix reports whether host is a public suffix itself: a registry
// suffix such as "co.jp" or a hosting suffix such as "github.io".
//
// Such a name cannot be somebody's own domain: every customer under that suffix
// would count as them, and the citation rate would be meaningless.
//
// A single label is only a suffix when it is a registry TLD: "com" is the whole
// world, while "localhost" is a local name with no sharing relationship.
func IsPublicSuffix(host string) bool {
	canonical := CanonicalHost(host)
	if canonical == "" {
		return false
	}
	suffix, icann := publicsuffix.PublicSuffix(canonical)
	if suffix != canonical {
		return false
	}
	if !strings.Contains(canonical, ".") {
		return icann
	}
	return true
}

// SplitCitationURL turns a raw citation URL into its normalised parts.
//
//   - the host is canonicalised (lower case, no trailing dot, punycode for
//     international labels: see CanonicalHost), so https://例え.テスト/ and
//     https://xn--r8jz45g.xn--zckzah/ are the same host
//   - query and fragment never take part in host matching or duplicate folding,
//     so ?utm_source=... does not create a second citation of the same page
//   - schemes other than http/https, relative URLs and URLs without a host are
//     malformed: they are kept in the record, not dropped
func SplitCitationURL(raw string) URLInfo {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return URLInfo{Malformed: true}
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return URLInfo{URL: trimmed, DedupeKey: trimmed, Malformed: true}
	}
	scheme := strings.ToLower(parsed.Scheme)
	host := CanonicalHost(parsed.Hostname())
	if (scheme != "http" && scheme != "https") || host == "" {
		return URLInfo{URL: trimmed, DedupeKey: trimmed, Malformed: true}
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	domain := host
	return URLInfo{
		URL:       trimmed,
		Domain:    &domain,
		DedupeKey: scheme + "://" + host + path,
	}
}

// IsOwnDomain reports whether domain belongs to ownDomain. ownDomain is matched
// exactly, or as a label-boundary suffix when subdomains is true: "sub.example.com"
// matches "example.com", while "notexample.com" never does. Both sides go through
// CanonicalHost first, so a Unicode own_domain matches a punycode citation.
func IsOwnDomain(domain string, ownDomain string, subdomains bool) bool {
	host := CanonicalHost(domain)
	own := CanonicalHost(ownDomain)
	if host == "" || own == "" {
		return false
	}
	if host == own {
		return true
	}
	return subdomains && strings.HasSuffix(host, "."+own)
}

// CitationDetail is one citation with the raw and the derived forms kept apart,
// which is what the citations table stores.
type CitationDetail struct {
	Position      int
	RawURL        string
	NormalizedURL string
	DedupeKey     string
	Domain        *string
	IsOwn         bool
	Malformed     bool
	// SourceHost is the host the provider reported alongside an opaque citation
	// URL, when it did (Gemini's grounding chunks carry a redirect URL and the
	// real source in the title). Empty means "derive it from the URL".
	SourceHost string
}

// CitationInput is one raw citation as a provider reported it.
type CitationInput struct {
	URL string
	// SourceHost is the provider's own hint about where the URL points, used only
	// when the URL itself is not the page (a grounding redirect).
	SourceHost string
}

// NormalizeCitationInputs folds duplicates and numbers the citations from 1.
//
// A citation whose URL is opaque (a provider redirect) but which carries a host
// hint keeps the raw URL and is classified by the hint: hostname is the hinted
// host, normalized_url is the raw URL (nothing about the redirect can be
// canonicalised), and the dedupe key is the raw URL. Without the hint every
// Gemini citation would look like vertexaisearch.cloud.google.com and no site
// could ever be "cited".
func NormalizeCitationInputs(inputs []CitationInput, ownDomain string, subdomains bool) []CitationDetail {
	details := make([]CitationDetail, 0, len(inputs))
	seen := make(map[string]bool, len(inputs))
	for _, input := range inputs {
		hint := CanonicalHost(input.SourceHost)
		if hint != "" && !looksLikeHost(hint) {
			hint = ""
		}
		if hint != "" {
			raw := strings.TrimSpace(input.URL)
			if raw == "" {
				continue
			}
			if seen[raw] {
				continue
			}
			seen[raw] = true
			domainValue := hint
			details = append(details, CitationDetail{
				Position:      len(details) + 1,
				RawURL:        raw,
				NormalizedURL: raw,
				DedupeKey:     raw,
				Domain:        &domainValue,
				IsOwn:         IsOwnDomain(domainValue, ownDomain, subdomains),
				SourceHost:    hint,
			})
			continue
		}
		info := SplitCitationURL(input.URL)
		if info.URL == "" && info.DedupeKey == "" {
			continue
		}
		if info.DedupeKey != "" {
			if seen[info.DedupeKey] {
				continue
			}
			seen[info.DedupeKey] = true
		}
		normalized := ""
		if !info.Malformed {
			normalized = info.DedupeKey
		}
		details = append(details, CitationDetail{
			Position:      len(details) + 1,
			RawURL:        info.URL,
			NormalizedURL: normalized,
			DedupeKey:     info.DedupeKey,
			Domain:        info.Domain,
			IsOwn:         IsOwnDomain(derefString(info.Domain), ownDomain, subdomains),
			Malformed:     info.Malformed,
		})
	}
	return details
}

// looksLikeHost rejects a title that happens to contain a dot but is a sentence
// ("Go 1.27 release"), so a provider's display title is never mistaken for a
// domain.
func looksLikeHost(value string) bool {
	if !strings.Contains(value, ".") {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '-' {
			continue
		}
		return false
	}
	return true
}

// NormalizeCitationDetails folds duplicates and numbers the citations from 1,
// keeping both the raw URL and the normalised form.
//
// normalized_url is the canonical identity of the page (scheme://host/path): the
// query and the fragment are what make the same page look like two citations. A
// malformed URL has no canonical form, so normalized_url is empty and malformed is
// true; it is still recorded.
func NormalizeCitationDetails(rawURLs []string, ownDomain string, subdomains bool) []CitationDetail {
	inputs := make([]CitationInput, 0, len(rawURLs))
	for _, raw := range rawURLs {
		inputs = append(inputs, CitationInput{URL: raw})
	}
	return NormalizeCitationInputs(inputs, ownDomain, subdomains)
}

func NormalizeCitations(rawURLs []string, ownDomain string, subdomains bool) []Citation {
	details := NormalizeCitationDetails(rawURLs, ownDomain, subdomains)
	citations := make([]Citation, 0, len(details))
	for _, detail := range details {
		citations = append(citations, Citation{
			Position:  detail.Position,
			URL:       detail.RawURL,
			Domain:    detail.Domain,
			IsOwn:     detail.IsOwn,
			Malformed: detail.Malformed,
		})
	}
	return citations
}

// BrandMatch is the outcome of matching one brand term against an answer.
type BrandMatch struct {
	// Term is the term as configured (not normalised), so a record says which
	// configured word hit.
	Term string
	// Occurrences is how many times the term appears.
	Occurrences int
	// Ambiguous is true when the term only appears inside a longer word, e.g. the
	// brand "Go" inside "Google". Such a hit is recorded but excluded from the
	// mention rate: it is unknown, not zero.
	Ambiguous bool
}

// MatchBrandTerm reports how one brand term appears in the answer text.
//
// Both sides go through NormalizeText first (NFKC, whitespace folded, lower
// case). A plain ASCII term is matched on word boundaries, so "Go" matches
// "Go 1.27.4" but not "Google"; a term that contains any non-ASCII character is
// matched as a substring, because those scripts have no boundary to anchor on.
// A term that is found only inside a longer word is returned as Ambiguous.
func MatchBrandTerm(answerText, term string) (BrandMatch, bool) {
	text := NormalizeText(answerText)
	needle := NormalizeText(term)
	if text == "" || needle == "" {
		return BrandMatch{}, false
	}
	occurrences := strings.Count(text, needle)
	if occurrences == 0 {
		return BrandMatch{}, false
	}
	match := BrandMatch{Term: term, Occurrences: occurrences}
	if !isASCIIWord(needle) {
		return match, true
	}
	if bounded := countBoundedOccurrences(text, needle); bounded > 0 {
		match.Occurrences = bounded
		return match, true
	}
	match.Ambiguous = true
	return match, true
}

// BrandMentioned reports whether any brand term is mentioned unambiguously.
func BrandMentioned(answerText string, brandTerms []string) bool {
	for _, term := range brandTerms {
		if match, ok := MatchBrandTerm(answerText, term); ok && !match.Ambiguous {
			return true
		}
	}
	return false
}

// isASCIIWord reports whether the term is plain lower-case ASCII letters, digits
// and spaces, the only shape for which a word boundary means something here.
func isASCIIWord(term string) bool {
	for i := 0; i < len(term); i++ {
		c := term[i]
		if c == ' ' || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			continue
		}
		return false
	}
	return true
}

// countBoundedOccurrences counts hits that are not part of a longer word.
// The text is already lower case, so a boundary is simply "not [a-z0-9]".
func countBoundedOccurrences(text, needle string) int {
	count := 0
	for start := 0; start+len(needle) <= len(text); {
		index := strings.Index(text[start:], needle)
		if index < 0 {
			break
		}
		pos := start + index
		end := pos + len(needle)
		if (pos == 0 || !isWordByte(text[pos-1])) && (end == len(text) || !isWordByte(text[end])) {
			count++
		}
		start = pos + 1
	}
	return count
}

func isWordByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}

// ClassifyDomain buckets a cited domain for reporting: own, competitor or other.
func ClassifyDomain(domain string, ownDomain string, subdomains bool, competitors []string) string {
	if domain == "" {
		return "other (malformed url)"
	}
	if IsOwnDomain(domain, ownDomain, subdomains) {
		return "own"
	}
	for _, competitor := range competitors {
		if IsOwnDomain(domain, competitor, subdomains) {
			return "competitor"
		}
	}
	return "other"
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// StringOrEmpty is the read-side twin of a nullable string column.
func StringOrEmpty(value *string) string { return derefString(value) }
