package sqlite

// Models are the read/write shapes of the schema. They are deliberately plain
// structs: provider JSON never reaches this package (adapters convert first).

// Project is the site under measurement.
type Project struct {
	ID         int64
	SiteID     string
	Name       string
	OwnDomain  string
	Subdomains bool
	Market     string
	Language   string
}

// Question is a stable question key plus its current text.
type Question struct {
	ID        int64
	ProjectID int64
	Key       string
	Text      string
	Category  string
	Language  string
	Active    bool
	Version   string
}

// Run is one attempted call with every condition attached.
type Run struct {
	ID                    int64
	ProjectID             int64
	QuestionID            int64
	QuestionKey           string
	QuestionVersion       string
	QuestionText          string
	Category              string
	RepeatIndex           int
	Provider              string
	Model                 string
	APIEndpointVersion    string
	SearchEnabled         bool
	MeasurementMethod     string
	Stage                 string
	QuestionSetVersion    string
	QuestionSetSHA256     string
	PlanKey               string
	Session               string
	StartedAtUTC          string
	CompletedAtUTC        string
	Status                string
	CitationObservability string
	CitedOwn              bool
	CitationCount         int
	BrandMentioned        bool
	ToolVersion           string
	InputTokens           *int
	OutputTokens          *int
	ToolCalls             *int
}

// ResponseRow is the stored answer plus usage snapshot.
type ResponseRow struct {
	RunID           int64
	AnswerText      string
	AnswerSHA256    string
	RawResponseJSON string
	// BodyStored is false when the run was recorded hash-only: the empty text is a
	// choice, not a lost answer.
	BodyStored            bool
	CitationObservability string
	ErrorCode             string
	ErrorMessage          string
	InputTokens           *int
	OutputTokens          *int
	ToolUsage             string
}

// CitationRow is one cited URL with both raw and derived values.
type CitationRow struct {
	RunID         int64
	ResponseID    int64
	Position      int
	RawURL        string
	NormalizedURL string
	DedupeKey     string
	Hostname      *string
	SourceType    string
	IsOwn         bool
	Malformed     bool
}

// MentionRow is one brand / competitor term hit.
type MentionRow struct {
	RunID       int64
	ResponseID  int64
	Term        string
	Kind        string
	MatchedText string
	Occurrences int
	Ambiguous   bool
}

// UsageRow is the provider-reported usage of a run (tokens and tool calls), not
// a cost: this tool does not price anything.
type UsageRow struct {
	RunID        int64
	Provider     string
	Model        string
	InputTokens  *int
	OutputTokens *int
	TotalTokens  *int
	ToolCalls    *int
}

// CitationWrite is a citation to append with its run.
type CitationWrite struct {
	Position      int
	RawURL        string
	NormalizedURL string
	DedupeKey     string
	Hostname      *string
	SourceType    string
	IsOwn         bool
	Malformed     bool
}

// MentionWrite is a mention to append with its run.
type MentionWrite struct {
	Term        string
	Kind        string
	MatchedText string
	Occurrences int
	Ambiguous   bool
}

// UsageWrite is the usage snapshot to append with its run.
type UsageWrite struct {
	InputTokens  *int
	OutputTokens *int
	TotalTokens  *int
	ToolCalls    *int
}

// RunWrite is one append-only run + response + children.
type RunWrite struct {
	ProjectID             int64
	QuestionID            int64
	QuestionVersion       string
	RepeatIndex           int
	Provider              string
	Model                 string
	APIEndpointVersion    string
	SearchEnabled         bool
	Stage                 string
	QuestionSetVersion    string
	QuestionSetSHA256     string
	PlanKey               string
	Session               string
	StartedAtUTC          string
	CompletedAtUTC        string
	Status                string
	CitationObservability string
	CitedOwn              bool
	CitationCount         int
	BrandMentioned        bool
	ToolVersion           string
	AnswerText            string
	AnswerSHA256          string
	RawResponseJSON       string
	// HashOnlyBody writes the hash and nothing else (no answer text, no raw JSON).
	HashOnlyBody bool
	ErrorCode    string
	ErrorMessage string
	Citations    []CitationWrite
	Mentions     []MentionWrite
	Usage        UsageWrite
}

// Counts is the cheap overview used by `aicm verify` and status output.
type Counts struct {
	Projects         int
	Questions        int
	QuestionVersions int
	QuestionSets     int
	Competitors      int
	Runs             int
	RunsValid        int
	Responses        int
	Citations        int
	Mentions         int
}

// Issue is one integrity finding produced by `aicm verify`.
type Issue struct {
	Kind   string
	Detail string
	RunID  int64
}
