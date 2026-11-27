// Package question parses the question-set inputs: a YAML contract and a CSV
// list. It validates before anything is written to the database, because a
// changed question text silently invalidates every earlier measurement of that
// question.
package question

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/routeflags/ai-citation-monitor/internal/domain"
)

// Categories is the closed vocabulary for a question category.
var Categories = []string{
	"definition", "comparison", "regulation", "effect", "safety",
	"usage", "purchase", "list", "identification", "dosage",
}

// StageNames is the closed vocabulary for a stage.
var StageNames = []string{"smoke", "initial", "regular"}

// BrandTermsStatus values accepted in a question set. "proposed" means the term
// list is still a guess: a mention rate depends on how the terms are chosen, so a
// report built from such a set has to say that the terms were not confirmed.
const (
	BrandTermsStatusProposed  = "proposed"
	BrandTermsStatusConfirmed = "confirmed"
)

// Question is one fixed question.
type Question struct {
	Key      string `yaml:"id" json:"question_key"`
	Text     string `yaml:"text" json:"text"`
	Category string `yaml:"category" json:"category"`
	Language string `yaml:"language" json:"language"`
	Version  string `yaml:"version" json:"version"`
}

// Stage is one execution size.
type Stage struct {
	RunsPerQuestion int      `yaml:"runs_per_question"`
	QuestionIDs     []string `yaml:"question_ids"`
}

// Set is a parsed question-set document.
type Set struct {
	Path             string           `yaml:"-"`
	SetVersion       string           `yaml:"set_version"`
	SiteID           string           `yaml:"site_id"`
	OwnDomain        string           `yaml:"own_domain"`
	Subdomains       *bool            `yaml:"subdomains"`
	Market           string           `yaml:"market"`
	Language         string           `yaml:"language"`
	BrandTerms       []string         `yaml:"brand_terms"`
	BrandTermsStatus string           `yaml:"brand_terms_status"`
	Competitors      []string         `yaml:"competitors"`
	Questions        []Question       `yaml:"questions"`
	Stages           map[string]Stage `yaml:"stages"`

	SHA256 string `yaml:"-"`
}

// LoadYAML reads and hashes a question-set file.
func LoadYAML(path string) (*Set, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read question set %s: %w", path, err)
	}
	var set Set
	if err := yaml.Unmarshal(data, &set); err != nil {
		return nil, fmt.Errorf("parse question set %s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	set.SHA256 = hex.EncodeToString(sum[:])
	set.Path = path
	if set.Stages == nil {
		set.Stages = map[string]Stage{}
	}
	return &set, nil
}

// HasSubdomains reports whether subdomains of own_domain count as own.
func (s *Set) HasSubdomains() bool {
	if s.Subdomains == nil {
		return true
	}
	return *s.Subdomains
}

// Validate returns the problems that make the set unusable (errors) and the ones
// worth a note (warnings).
func (s *Set) Validate(strict bool) (errorsOut []string, warnings []string) {
	err := func(code, message string) { errorsOut = append(errorsOut, code+": "+message) }
	warn := func(code, message string) { warnings = append(warnings, code+": "+message) }

	if strings.TrimSpace(s.SetVersion) == "" {
		err("E010", "set_version is empty (raise it whenever a question text changes)")
	}
	if strings.TrimSpace(s.SiteID) == "" {
		err("E011", "site_id is empty")
	}
	own := strings.TrimSpace(s.OwnDomain)
	if own == "" {
		err("E012", "own_domain is empty (needed to decide whether a citation is yours)")
	} else if strings.ContainsAny(own, "/:") {
		err("E012", fmt.Sprintf("own_domain must be a bare host name, got %q", own))
	}
	if strings.TrimSpace(s.Market) == "" {
		err("E013", "market is empty (the same question in another market is another measurement)")
	}

	if len(s.Questions) == 0 {
		err("E020", "questions is empty")
	}
	seenIDs := map[string]bool{}
	seenText := map[string]string{}
	categorySet := map[string]bool{}
	for _, category := range Categories {
		categorySet[category] = true
	}
	for index, question := range s.Questions {
		where := fmt.Sprintf("questions[%d]", index)
		key := strings.TrimSpace(question.Key)
		if key == "" {
			err("E025", where+" has no id")
		} else if seenIDs[key] {
			err("E021", "duplicate id: "+key)
		}
		seenIDs[key] = true

		text := strings.TrimSpace(question.Text)
		if text == "" {
			err("E023", where+" ("+key+") has empty text")
		} else {
			normalized := domain.NormalizeText(text)
			if other, ok := seenText[normalized]; ok {
				err("E024", "two questions normalise to the same text: "+key+" and "+other)
			} else {
				seenText[normalized] = key
			}
		}
		if !categorySet[strings.TrimSpace(question.Category)] {
			err("E022", fmt.Sprintf("%s (%s) has an unknown category %q (allowed: %s)",
				where, key, question.Category, strings.Join(Categories, ", ")))
		}
	}

	if len(s.Stages) == 0 {
		err("E030", "stages is empty")
	}
	allowedStage := map[string]bool{}
	for _, name := range StageNames {
		allowedStage[name] = true
	}
	for name, stage := range s.Stages {
		if !allowedStage[name] {
			err("E030", fmt.Sprintf("unknown stage %q (allowed: %s)", name, strings.Join(StageNames, ", ")))
			continue
		}
		if len(stage.QuestionIDs) == 0 {
			err("E032", "stage "+name+" has no question_ids")
		}
		if stage.RunsPerQuestion < 1 || stage.RunsPerQuestion > 5 {
			err("E033", fmt.Sprintf("stage %s runs_per_question must be 1..5, got %d", name, stage.RunsPerQuestion))
		}
		seen := map[string]bool{}
		for _, id := range stage.QuestionIDs {
			if !seenIDs[id] {
				err("E031", "stage "+name+" references unknown id "+id)
			}
			if seen[id] {
				err("E031", "stage "+name+" lists "+id+" twice")
			}
			seen[id] = true
		}
	}

	order := []string{}
	for _, name := range StageNames {
		if _, ok := s.Stages[name]; ok {
			order = append(order, name)
		}
	}
	for index := 0; index+1 < len(order); index++ {
		outer := order[index]
		inner := order[index+1]
		outerIDs := s.Stages[outer].QuestionIDs
		innerSet := map[string]bool{}
		for _, id := range s.Stages[inner].QuestionIDs {
			innerSet[id] = true
		}
		missing := []string{}
		for _, id := range outerIDs {
			if !innerSet[id] {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			message := fmt.Sprintf("stage %s questions are missing from %s: %s", outer, inner, strings.Join(missing, ", "))
			if strict {
				err("E040", message)
			} else {
				warn("W040", message)
			}
		}
	}

	if len(s.BrandTerms) == 0 {
		warn("W041", "brand_terms is empty: the brand mention rate cannot be measured")
	}
	status := strings.TrimSpace(s.BrandTermsStatus)
	if status != "" && status != BrandTermsStatusProposed && status != BrandTermsStatusConfirmed {
		err("E043", fmt.Sprintf("brand_terms_status must be proposed or confirmed, got %q", status))
	}
	if len(s.Competitors) == 0 {
		warnings = append(warnings, "I042: competitors is empty (the share table shows own / other only)")
	}
	return errorsOut, warnings
}

// StageRoundTrips is questions x runs for one stage (one provider).
func (s *Set) StageRoundTrips(stage string) int {
	definition, ok := s.Stages[stage]
	if !ok {
		return 0
	}
	return len(definition.QuestionIDs) * definition.RunsPerQuestion
}

// StageQuestions returns the questions of a stage in declared order.
func (s *Set) StageQuestions(stage string) []Question {
	definition, ok := s.Stages[stage]
	if !ok {
		return nil
	}
	byKey := map[string]Question{}
	for _, question := range s.Questions {
		byKey[question.Key] = question
	}
	result := make([]Question, 0, len(definition.QuestionIDs))
	for _, key := range definition.QuestionIDs {
		if question, ok := byKey[key]; ok {
			result = append(result, question)
		}
	}
	return result
}

// Summary is the human-readable view printed by `questions import --check`.
type Summary struct {
	SetVersion       string         `json:"set_version"`
	SiteID           string         `json:"site_id"`
	OwnDomain        string         `json:"own_domain"`
	Market           string         `json:"market"`
	BrandTermsStatus string         `json:"brand_terms_status"`
	SHA256           string         `json:"sha256"`
	Questions        int            `json:"questions"`
	BrandTerms       int            `json:"brand_terms"`
	Competitors      int            `json:"competitors"`
	Stages           []StageSummary `json:"stages"`
}

// StageSummary is one stage line of the summary.
type StageSummary struct {
	Name            string `json:"name"`
	Questions       int    `json:"questions"`
	RunsPerQuestion int    `json:"runs_per_question"`
	RoundTrips      int    `json:"round_trips_per_provider"`
}

// Summarise builds the display summary.
func (s *Set) Summarise() Summary {
	summary := Summary{
		SetVersion:       s.SetVersion,
		SiteID:           s.SiteID,
		OwnDomain:        s.OwnDomain,
		Market:           s.Market,
		BrandTermsStatus: strings.TrimSpace(s.BrandTermsStatus),
		SHA256:           s.SHA256,
		Questions:        len(s.Questions),
		BrandTerms:       len(s.BrandTerms),
		Competitors:      len(s.Competitors),
	}
	names := make([]string, 0, len(s.Stages))
	for name := range s.Stages {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		definition := s.Stages[name]
		summary.Stages = append(summary.Stages, StageSummary{
			Name:            name,
			Questions:       len(definition.QuestionIDs),
			RunsPerQuestion: definition.RunsPerQuestion,
			RoundTrips:      len(definition.QuestionIDs) * definition.RunsPerQuestion,
		})
	}
	return summary
}

// ParseCSV reads questions from a CSV with the header id,text,category[,language][,version].
// The header is required: guessing column order is how imports get silently wrong.
func ParseCSV(reader io.Reader) ([]Question, error) {
	parser := csv.NewReader(reader)
	parser.FieldsPerRecord = -1
	records, err := parser.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read csv: %w", err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("csv is empty")
	}
	header := make([]string, 0, len(records[0]))
	for _, column := range records[0] {
		header = append(header, strings.ToLower(strings.TrimSpace(strings.TrimPrefix(column, "\ufeff"))))
	}
	index := map[string]int{}
	for position, column := range header {
		index[column] = position
	}
	for _, required := range []string{"id", "text", "category"} {
		if _, ok := index[required]; !ok {
			return nil, fmt.Errorf("csv header must contain %q (got: %s)", required, strings.Join(header, ","))
		}
	}

	questions := make([]Question, 0, len(records)-1)
	for line, record := range records[1:] {
		if len(record) == 0 || strings.TrimSpace(strings.Join(record, "")) == "" {
			continue
		}
		get := func(column string) string {
			position, ok := index[column]
			if !ok || position >= len(record) {
				return ""
			}
			return strings.TrimSpace(record[position])
		}
		if get("id") == "" || get("text") == "" {
			return nil, fmt.Errorf("csv line %d: id and text are required", line+2)
		}
		questions = append(questions, Question{
			Key:      get("id"),
			Text:     get("text"),
			Category: get("category"),
			Language: get("language"),
			Version:  get("version"),
		})
	}
	if len(questions) == 0 {
		return nil, fmt.Errorf("csv has no question rows")
	}
	return questions, nil
}
