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

	"golang.org/x/text/unicode/norm"
)

// NormalizerVersion is the version of the normalisation and matching rules.
// Bump it whenever URL normalisation (query/fragment handling, trailing dot,
// subdomain matching) or brand matching (NFKC) changes: cited_own and
// brand_mentioned would change meaning, and records must stay comparable only
// within one version.
const NormalizerVersion = "1"

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

// SplitCitationURL turns a raw citation URL into its normalised parts.
//
//   - the host is lower-cased and a trailing dot is removed (example.com. -> example.com)
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
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
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
// matches "example.com", while "notexample.com" never does.
func IsOwnDomain(domain string, ownDomain string, subdomains bool) bool {
	if domain == "" || ownDomain == "" {
		return false
	}
	own := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(ownDomain), "."))
	host := strings.ToLower(strings.TrimSuffix(domain, "."))
	if own == "" {
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
}

// NormalizeCitationDetails folds duplicates and numbers the citations from 1,
// keeping both the raw URL and the normalised form.
//
// normalized_url is the canonical identity of the page (scheme://host/path): the
// query and the fragment are what make the same page look like two citations. A
// malformed URL has no canonical form, so normalized_url is empty and malformed is
// true; it is still recorded.
func NormalizeCitationDetails(rawURLs []string, ownDomain string, subdomains bool) []CitationDetail {
	details := make([]CitationDetail, 0, len(rawURLs))
	seen := make(map[string]bool, len(rawURLs))
	for _, raw := range rawURLs {
		info := SplitCitationURL(raw)
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

// NormalizeCitations folds duplicates and numbers the citations from 1.
// Empty URLs are not citations and are skipped (they must not inflate counts).
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

// BrandMentioned reports whether any brand term occurs in the answer text.
// Matching uses NormalizeText on both sides, so "THCH Vape" matches "thch  vape".
func BrandMentioned(answerText string, brandTerms []string) bool {
	text := NormalizeText(answerText)
	if text == "" {
		return false
	}
	for _, term := range brandTerms {
		normalized := NormalizeText(term)
		if normalized != "" && strings.Contains(text, normalized) {
			return true
		}
	}
	return false
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
