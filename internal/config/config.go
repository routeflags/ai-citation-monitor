// Package config loads the per-site TOML configuration.
//
// The configuration never holds a credential value: it holds the *name* of the
// environment variable that carries it. The tool reads the variable at call time
// and never writes it to the database, the log or an error message.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/routeflags/ai-citation-monitor/internal/domain"
	"github.com/routeflags/ai-citation-monitor/internal/providers/anthropic"
)

// Defaults for the values a user does not have to write.
const (
	DefaultDBPath        = "measurements/aicm.db"
	DefaultReportDir     = "reports"
	DefaultMarket        = "ja-JP"
	DefaultLanguage      = "ja"
	DefaultConcurrency   = 2
	DefaultRPM           = 20
	DefaultMaxRetries    = 3
	DefaultTimeoutSecond = 90
)

// ProviderConfig is the per-provider block. BaseURL is overridable so tests can
// point the adapters at httptest without changing code.
type ProviderConfig struct {
	Model          string `toml:"model"`
	APIKeyEnv      string `toml:"api_key_env"`
	BaseURL        string `toml:"base_url"`
	TimeoutSeconds int    `toml:"timeout_seconds"`
}

// AnthropicConfig is the Claude block. The Messages API requires max_tokens and
// the server-side web search tool takes max_uses, so both are settings rather
// than constants (the adapter has the same values as its fallbacks).
type AnthropicConfig struct {
	Model          string `toml:"model"`
	APIKeyEnv      string `toml:"api_key_env"`
	BaseURL        string `toml:"base_url"`
	TimeoutSeconds int    `toml:"timeout_seconds"`
	MaxTokens      int    `toml:"max_tokens"`
	MaxUses        int    `toml:"max_uses"`
}

// Providers groups the adapters this build knows about.
type Providers struct {
	OpenAI     ProviderConfig  `toml:"openai"`
	Gemini     ProviderConfig  `toml:"gemini"`
	Anthropic  AnthropicConfig `toml:"anthropic"`
	Perplexity ProviderConfig  `toml:"perplexity"`
	Grok       ProviderConfig  `toml:"grok"`
}

// Limits bounds concurrency, request rate and retries.
type Limits struct {
	Concurrency int `toml:"concurrency"`
	RPM         int `toml:"rpm"`
	MaxRetries  int `toml:"max_retries"`
}

// ReportSettings controls the generated report.
type ReportSettings struct {
	Language string `toml:"language"`
}

// Config is one site's configuration.
type Config struct {
	SiteID      string   `toml:"site_id"`
	SiteName    string   `toml:"site_name"`
	OwnDomain   string   `toml:"own_domain"`
	Subdomains  *bool    `toml:"subdomains"`
	Market      string   `toml:"market"`
	Language    string   `toml:"language"`
	QuestionSet string   `toml:"question_set"`
	DB          string   `toml:"db"`
	ReportDir   string   `toml:"report_dir"`
	BrandTerms  []string `toml:"brand_terms"`
	Competitors []string `toml:"competitors"`
	// StoreAnswerBody keeps the answer text and the raw JSON in the database. When
	// false, only the answer hash is written (the C1 contract's O20: the answer is
	// not kept in a Git-tracked database). Nil means "not set": keep the body.
	StoreAnswerBody *bool `toml:"store_answer_body"`

	Report    ReportSettings `toml:"report"`
	Providers Providers      `toml:"providers"`
	Limits    Limits         `toml:"limits"`

	// Path is the file this config came from ("" when it was not found).
	Path string `toml:"-"`
}

// HasAnswerBody reports whether the answer text and the raw JSON are stored.
func (c *Config) HasAnswerBody() bool {
	return c.StoreAnswerBody == nil || *c.StoreAnswerBody
}

// HasSubdomains reports whether subdomains of own_domain count as own.
func (c *Config) HasSubdomains() bool {
	if c.Subdomains == nil {
		return true
	}
	return *c.Subdomains
}

// Provider returns the block for a provider name
// (openai / gemini / anthropic / perplexity / grok).
func (c *Config) Provider(name string) (ProviderConfig, bool) {
	switch name {
	case "openai":
		return c.Providers.OpenAI, true
	case "gemini":
		return c.Providers.Gemini, true
	case "grok":
		return c.Providers.Grok, true
	case "anthropic":
		// The shared view of the block; the two Claude-only numbers stay available
		// through c.Providers.Anthropic.
		return ProviderConfig{
			Model:          c.Providers.Anthropic.Model,
			APIKeyEnv:      c.Providers.Anthropic.APIKeyEnv,
			BaseURL:        c.Providers.Anthropic.BaseURL,
			TimeoutSeconds: c.Providers.Anthropic.TimeoutSeconds,
		}, true
	case "perplexity":
		return c.Providers.Perplexity, true
	default:
		return ProviderConfig{}, false
	}
}

// ProviderNames lists the providers this build supports, in a stable order. A new
// provider is appended: the order is part of what an operator sees in `aicm
// version`, the help text and the error messages, and re-ordering old entries
// would make previously printed lists and records look different.
func ProviderNames() []string {
	return []string{"openai", "gemini", "anthropic", "perplexity", "grok"}
}

// Load reads the configuration. A missing file is not an error: it returns the
// defaults (with Path empty) and a warning so callers can print an actionable
// message. Warnings never contain a secret value.
func Load(path string) (*Config, []string, error) {
	cfg := &Config{}
	warnings := []string{}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg.ApplyDefaults()
			warnings = append(warnings, fmt.Sprintf("config %s not found; using defaults (run `aicm init`)", path))
			return cfg, warnings, nil
		}
		return nil, nil, fmt.Errorf("read config %s: %w", path, err)
	}

	if _, err := toml.Decode(string(data), cfg); err != nil {
		return nil, warnings, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.Path = path
	cfg.ApplyDefaults()
	warnings = append(warnings, secretWarnings(cfg)...)
	return cfg, warnings, nil
}

// ApplyDefaults fills the values a user does not have to write.
func (c *Config) ApplyDefaults() {
	if c.Market == "" {
		c.Market = DefaultMarket
	}
	if c.Language == "" {
		c.Language = DefaultLanguage
	}
	if c.DB == "" {
		c.DB = DefaultDBPath
	}
	if c.ReportDir == "" {
		c.ReportDir = DefaultReportDir
	}
	if c.Limits.Concurrency <= 0 {
		c.Limits.Concurrency = DefaultConcurrency
	}
	if c.Limits.RPM <= 0 {
		c.Limits.RPM = DefaultRPM
	}
	// 0 means "not set": three retries. Set -1 to disable retries entirely.
	if c.Limits.MaxRetries == 0 {
		c.Limits.MaxRetries = DefaultMaxRetries
	}
	// The report language follows the site language unless [report] language says
	// otherwise: an operator who sets --language ja expects Japanese numbers, not
	// an English report that disagrees with the question set.
	if c.Report.Language == "" {
		if c.Language != "" {
			c.Report.Language = c.Language
		} else {
			c.Report.Language = "en"
		}
	}
	if c.Providers.OpenAI.APIKeyEnv == "" {
		c.Providers.OpenAI.APIKeyEnv = "OPENAI_API_KEY"
	}
	if c.Providers.Gemini.APIKeyEnv == "" {
		c.Providers.Gemini.APIKeyEnv = "GEMINI_API_KEY"
	}
	if c.Providers.OpenAI.BaseURL == "" {
		c.Providers.OpenAI.BaseURL = "https://api.openai.com/v1"
	}
	if c.Providers.Gemini.BaseURL == "" {
		c.Providers.Gemini.BaseURL = "https://generativelanguage.googleapis.com/v1beta"
	}
	if c.Providers.OpenAI.TimeoutSeconds <= 0 {
		c.Providers.OpenAI.TimeoutSeconds = DefaultTimeoutSecond
	}
	if c.Providers.Gemini.TimeoutSeconds <= 0 {
		c.Providers.Gemini.TimeoutSeconds = DefaultTimeoutSecond
	}
	if c.Providers.Anthropic.APIKeyEnv == "" {
		c.Providers.Anthropic.APIKeyEnv = "ANTHROPIC_API_KEY"
	}
	if c.Providers.Anthropic.BaseURL == "" {
		c.Providers.Anthropic.BaseURL = "https://api.anthropic.com"
	}
	if c.Providers.Anthropic.TimeoutSeconds <= 0 {
		c.Providers.Anthropic.TimeoutSeconds = DefaultTimeoutSecond
	}
	if c.Providers.Anthropic.MaxTokens <= 0 {
		c.Providers.Anthropic.MaxTokens = anthropic.DefaultMaxTokens
	}
	if c.Providers.Anthropic.MaxUses <= 0 {
		c.Providers.Anthropic.MaxUses = anthropic.DefaultMaxUses
	}
	if c.Providers.Perplexity.APIKeyEnv == "" {
		c.Providers.Perplexity.APIKeyEnv = "PERPLEXITY_API_KEY"
	}
	if c.Providers.Perplexity.BaseURL == "" {
		// The Agent API is the surface this build speaks (`/agent` is appended by the
		// adapter); `/v1/responses` is only an OpenAI-compatible alias.
		c.Providers.Perplexity.BaseURL = "https://api.perplexity.ai/v1"
	}
	if c.Providers.Perplexity.TimeoutSeconds <= 0 {
		c.Providers.Perplexity.TimeoutSeconds = DefaultTimeoutSecond
	}
	if c.Providers.Grok.APIKeyEnv == "" {
		c.Providers.Grok.APIKeyEnv = "XAI_API_KEY"
	}
	if c.Providers.Grok.BaseURL == "" {
		c.Providers.Grok.BaseURL = "https://api.x.ai/v1"
	}
	if c.Providers.Grok.TimeoutSeconds <= 0 {
		c.Providers.Grok.TimeoutSeconds = DefaultTimeoutSecond
	}
	// The model name is never defaulted: a guessed model is a measurement of
	// something the operator did not choose (see docs/providers.md).
}

// Problems lists what prevents a measurement from running. Each entry names the
// setting and how to fix it (exit code 2).
func (c *Config) Problems() []string {
	problems := []string{}
	if strings.TrimSpace(c.SiteID) == "" {
		problems = append(problems, "site_id is empty (set it in the config or with --site-id)")
	}
	own := strings.TrimSpace(c.OwnDomain)
	if own == "" {
		problems = append(problems, "own_domain is empty (needed to decide whether a citation is yours)")
	} else if strings.ContainsAny(own, "/:") {
		problems = append(problems, fmt.Sprintf("own_domain must be a bare host name, got %q", own))
	} else if domain.IsPublicSuffix(own) {
		// A public suffix is shared by unrelated organisations (github.io, co.jp), so
		// treating it as ours would count every customer of that suffix as ours and
		// inflate the citation rate without limit.
		problems = append(problems, fmt.Sprintf(
			"own_domain %q is a public suffix, so every site under it would count as yours; use your registrable domain instead (for a hosted suffix, your own subdomain such as mysite.%s)",
			own, domain.CanonicalHost(own)))
	}
	if strings.TrimSpace(c.Market) == "" {
		problems = append(problems, "market is empty (the same question in another market is a different measurement)")
	}
	switch c.Report.Language {
	case "", "en", "ja":
	default:
		problems = append(problems, fmt.Sprintf(
			"report language %q is not supported; use \"en\" or \"ja\" (or drop [report] language to follow the site language %q)",
			c.Report.Language, c.Language))
	}
	return problems
}

var envName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// secretWarnings flags configuration keys that look like they hold a credential
// value instead of the name of an environment variable. The value is never
// echoed.
func secretWarnings(cfg *Config) []string {
	if cfg.Path == "" {
		return nil
	}
	data, err := os.ReadFile(cfg.Path)
	if err != nil {
		return nil
	}
	var raw map[string]any
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return nil
	}
	var warnings []string
	walkConfig("", raw, func(key string, value any) {
		text, ok := value.(string)
		if !ok {
			return
		}
		lower := strings.ToLower(key)
		looksSecret := strings.Contains(lower, "api_key") || strings.Contains(lower, "apikey") ||
			strings.Contains(lower, "token") || strings.Contains(lower, "secret") ||
			strings.Contains(lower, "password") || strings.Contains(lower, "bearer")
		if !looksSecret {
			return
		}
		if envName.MatchString(text) {
			return // an environment variable name: that is what we want
		}
		warnings = append(warnings, fmt.Sprintf(
			"%s looks like a credential value, not an environment variable name; put the value in the environment and keep only the variable name here",
			key,
		))
	})
	sort.Strings(warnings)
	return warnings
}

func walkConfig(prefix string, node map[string]any, visit func(key string, value any)) {
	for key, value := range node {
		full := key
		if prefix != "" {
			full = prefix + "." + key
		}
		if nested, ok := value.(map[string]any); ok {
			walkConfig(full, nested, visit)
			continue
		}
		visit(full, value)
	}
}

// Save writes the configuration as TOML with comments. It never writes a
// credential value (only api_key_env names).
func Save(path string, cfg *Config) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	var b strings.Builder
	b.WriteString("# ai-citation-monitor configuration\n")
	b.WriteString("# Credentials are NOT stored here: only the name of the environment variable.\n\n")
	fmt.Fprintf(&b, "site_id = %q\n", cfg.SiteID)
	if cfg.SiteName != "" {
		fmt.Fprintf(&b, "site_name = %q\n", cfg.SiteName)
	}
	fmt.Fprintf(&b, "own_domain = %q\n", cfg.OwnDomain)
	fmt.Fprintf(&b, "subdomains = %t\n", cfg.HasSubdomains())
	fmt.Fprintf(&b, "market = %q\n", cfg.Market)
	fmt.Fprintf(&b, "language = %q\n", cfg.Language)
	if cfg.QuestionSet != "" {
		fmt.Fprintf(&b, "question_set = %q\n", cfg.QuestionSet)
	}
	fmt.Fprintf(&b, "db = %q\n", cfg.DB)
	fmt.Fprintf(&b, "report_dir = %q\n", cfg.ReportDir)
	fmt.Fprintf(&b, "brand_terms = %s\n", tomlStringSlice(cfg.BrandTerms))
	fmt.Fprintf(&b, "competitors = %s\n", tomlStringSlice(cfg.Competitors))
	b.WriteString("# false keeps only the answer hash: the text and the raw response are not\n")
	b.WriteString("# written, for a database that is committed (C1 contract O20).\n")
	fmt.Fprintf(&b, "store_answer_body = %t\n\n", cfg.HasAnswerBody())

	b.WriteString("[report]\n")
	fmt.Fprintf(&b, "language = %q  # en or ja; the CLI and the help stay English\n\n", cfg.Report.Language)

	b.WriteString("[providers.openai]\n")
	fmt.Fprintf(&b, "model = %q\n", cfg.Providers.OpenAI.Model)
	fmt.Fprintf(&b, "api_key_env = %q\n", cfg.Providers.OpenAI.APIKeyEnv)
	fmt.Fprintf(&b, "base_url = %q\n", cfg.Providers.OpenAI.BaseURL)
	fmt.Fprintf(&b, "timeout_seconds = %d\n\n", cfg.Providers.OpenAI.TimeoutSeconds)

	b.WriteString("[providers.gemini]\n")
	fmt.Fprintf(&b, "model = %q\n", cfg.Providers.Gemini.Model)
	fmt.Fprintf(&b, "api_key_env = %q\n", cfg.Providers.Gemini.APIKeyEnv)
	fmt.Fprintf(&b, "base_url = %q\n", cfg.Providers.Gemini.BaseURL)
	fmt.Fprintf(&b, "timeout_seconds = %d\n\n", cfg.Providers.Gemini.TimeoutSeconds)

	b.WriteString("[providers.anthropic]\n")
	fmt.Fprintf(&b, "model = %q\n", cfg.Providers.Anthropic.Model)
	fmt.Fprintf(&b, "api_key_env = %q\n", cfg.Providers.Anthropic.APIKeyEnv)
	fmt.Fprintf(&b, "base_url = %q\n", cfg.Providers.Anthropic.BaseURL)
	fmt.Fprintf(&b, "timeout_seconds = %d\n", cfg.Providers.Anthropic.TimeoutSeconds)
	fmt.Fprintf(&b, "max_tokens = %d  # required; a cut-off answer is an error\n", cfg.Providers.Anthropic.MaxTokens)
	fmt.Fprintf(&b, "max_uses = %d  # searches one call may run\n\n", cfg.Providers.Anthropic.MaxUses)

	b.WriteString("[providers.perplexity]\n")
	b.WriteString("# Agent API + the server-side web_search tool.\n")
	fmt.Fprintf(&b, "model = %q\n", cfg.Providers.Perplexity.Model)
	fmt.Fprintf(&b, "api_key_env = %q\n", cfg.Providers.Perplexity.APIKeyEnv)
	fmt.Fprintf(&b, "base_url = %q\n", cfg.Providers.Perplexity.BaseURL)
	fmt.Fprintf(&b, "timeout_seconds = %d\n\n", cfg.Providers.Perplexity.TimeoutSeconds)

	b.WriteString("[providers.grok]\n")
	b.WriteString("# xAI Responses API + the server-side web_search tool.\n")
	fmt.Fprintf(&b, "model = %q\n", cfg.Providers.Grok.Model)
	fmt.Fprintf(&b, "api_key_env = %q\n", cfg.Providers.Grok.APIKeyEnv)
	fmt.Fprintf(&b, "base_url = %q\n", cfg.Providers.Grok.BaseURL)
	fmt.Fprintf(&b, "timeout_seconds = %d\n\n", cfg.Providers.Grok.TimeoutSeconds)
	b.WriteString("[limits]\n")
	fmt.Fprintf(&b, "concurrency = %d\n", cfg.Limits.Concurrency)
	fmt.Fprintf(&b, "rpm = %d\n", cfg.Limits.RPM)
	fmt.Fprintf(&b, "max_retries = %d\n\n", cfg.Limits.MaxRetries)

	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func tomlStringSlice(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, fmt.Sprintf("%q", value))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
