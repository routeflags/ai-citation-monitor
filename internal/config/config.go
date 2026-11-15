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

// Providers groups the adapters this build knows about.
type Providers struct {
	OpenAI ProviderConfig `toml:"openai"`
	Gemini ProviderConfig `toml:"gemini"`
}

// Budget bounds spending. MaxUSD = 0 means "not set".
type Budget struct {
	MaxUSD   float64 `toml:"max_usd"`
	Currency string  `toml:"currency"`
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

// Price is the token price table entry for one provider/model.
type Price struct {
	InputPerMTok        float64 `toml:"input_usd_per_mtok"`
	OutputPerMTok       float64 `toml:"output_usd_per_mtok"`
	InputTokensPerCall  int     `toml:"input_tokens_per_call"`
	OutputTokensPerCall int     `toml:"output_tokens_per_call"`
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

	Report    ReportSettings   `toml:"report"`
	Providers Providers        `toml:"providers"`
	Budget    Budget           `toml:"budget"`
	Limits    Limits           `toml:"limits"`
	Pricing   map[string]Price `toml:"pricing"`

	// Path is the file this config came from ("" when it was not found).
	Path string `toml:"-"`
}

// HasSubdomains reports whether subdomains of own_domain count as own.
func (c *Config) HasSubdomains() bool {
	if c.Subdomains == nil {
		return true
	}
	return *c.Subdomains
}

// Provider returns the block for a provider name (openai / gemini).
func (c *Config) Provider(name string) (ProviderConfig, bool) {
	switch name {
	case "openai":
		return c.Providers.OpenAI, true
	case "gemini":
		return c.Providers.Gemini, true
	default:
		return ProviderConfig{}, false
	}
}

// ProviderNames lists the providers this build supports, in a stable order.
func ProviderNames() []string { return []string{"openai", "gemini"} }

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
	if c.Budget.Currency == "" {
		c.Budget.Currency = "USD"
	}
	if c.Report.Language == "" {
		c.Report.Language = "en"
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
	}
	if strings.TrimSpace(c.Market) == "" {
		problems = append(problems, "market is empty (the same question in another market is a different measurement)")
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
	fmt.Fprintf(&b, "competitors = %s\n\n", tomlStringSlice(cfg.Competitors))

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

	b.WriteString("[budget]\n")
	fmt.Fprintf(&b, "max_usd = %g\n", cfg.Budget.MaxUSD)
	fmt.Fprintf(&b, "currency = %q\n\n", cfg.Budget.Currency)

	b.WriteString("[limits]\n")
	fmt.Fprintf(&b, "concurrency = %d\n", cfg.Limits.Concurrency)
	fmt.Fprintf(&b, "rpm = %d\n", cfg.Limits.RPM)
	fmt.Fprintf(&b, "max_retries = %d\n\n", cfg.Limits.MaxRetries)

	if len(cfg.Pricing) > 0 {
		b.WriteString("# Token prices, used only for the cost estimate. Remove an entry and the\n")
		b.WriteString("# estimate becomes `unknown` instead of zero.\n")
		keys := make([]string, 0, len(cfg.Pricing))
		for key := range cfg.Pricing {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			price := cfg.Pricing[key]
			fmt.Fprintf(&b, "[pricing.%q]\n", key)
			fmt.Fprintf(&b, "input_usd_per_mtok = %g\n", price.InputPerMTok)
			fmt.Fprintf(&b, "output_usd_per_mtok = %g\n", price.OutputPerMTok)
			if price.InputTokensPerCall > 0 {
				fmt.Fprintf(&b, "input_tokens_per_call = %d\n", price.InputTokensPerCall)
			}
			if price.OutputTokensPerCall > 0 {
				fmt.Fprintf(&b, "output_tokens_per_call = %d\n", price.OutputTokensPerCall)
			}
			b.WriteString("\n")
		}
	}

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
