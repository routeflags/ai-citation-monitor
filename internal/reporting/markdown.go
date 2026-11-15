package reporting

import (
	"fmt"
	"strings"

	"github.com/routeflags/ai-citation-monitor/internal/analysis"
	"github.com/routeflags/ai-citation-monitor/internal/config"
	"github.com/routeflags/ai-citation-monitor/internal/i18n"
)

// SampleStatementEN and SampleStatementJA are invariant 9: the report says what
// kind of number this is. The statement is emitted at the top of every report and
// cannot be turned off.
const (
	SampleStatementEN = "> **These numbers are measurements of a sample.** An AI service does not expose its whole answer log, so every citation rate and mention rate here is the observation of \"this question set, this model, this many runs\". They do not represent the consumer ChatGPT / Gemini UI or every user's answer.\n"
	SampleStatementJA = "> **これは標本についての測定です。** AI サービスの全回答ログは取得できないため、ここに出る引用率・言及率は「この質問セットを・このモデルで・この回数実行した」範囲の観測です。ChatGPT / Gemini の一般向け画面や全ユーザーの結果を代表しません。\n"
)

// Options carry the report-time rules and labels.
type Options struct {
	Config      *config.Config
	Language    i18n.Lang
	Compare     *analysis.Result
	Trends      []analysis.TrendSeries
	Interval    string
	GeneratedAt string
}

// Build renders the report for one period.
func Build(result *analysis.Result, opts Options) string {
	lang := opts.Language
	if lang == "" {
		lang = i18n.EN
	}
	text := labelsFor(lang)
	var b strings.Builder
	period := result.Period

	fmt.Fprintf(&b, "# "+text.title+"\n\n", period.Label)
	if lang == i18n.JA {
		b.WriteString(SampleStatementJA)
	} else {
		b.WriteString(SampleStatementEN)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, text.generated+"\n\n", opts.GeneratedAt, zeroAsDash(period.StartUTC), zeroAsDash(period.EndUTC))

	writeConditions(&b, text, result, opts)
	writeObserved(&b, text, result)
	writeDerived(&b, text, lang, result, opts)
	if opts.Compare != nil {
		writeCompare(&b, text, lang, result, opts)
	}
	if len(opts.Trends) > 0 {
		writeTrends(&b, text, lang, opts.Trends, opts.Interval)
	}
	writeLimits(&b, text)
	return b.String()
}

func writeConditions(b *strings.Builder, text labels, result *analysis.Result, opts Options) {
	conditions := result.Conditions
	b.WriteString("## " + text.conditionsH + "\n\n")
	b.WriteString(text.conditionsNote + "\n\n")
	writeTable(b, []string{text.item, text.value}, [][]string{
		{text.provider, join(text, conditions.Providers)},
		{text.model, join(text, conditions.Models)},
		{text.stage, join(text, conditions.Stages)},
		{text.endpoint, join(text, conditions.EndpointVersions)},
		{text.search, boolText(conditions.SearchEnabled)},
		{text.method, join(text, conditions.MeasurementMethods)},
		{text.qsVersion, join(text, conditions.QuestionSetVersions)},
		{text.qsSha, shortHashes(text, conditions.QuestionSetSHA256s)},
		{text.normVersion, join(text, conditions.ToolVersions)},
		{text.observedRange, zeroAsDash(conditions.ObservedFirst) + " - " + zeroAsDash(conditions.ObservedLast)},
	})
	b.WriteString("\n")

	if opts.Config == nil {
		return
	}
	b.WriteString("**" + text.configH + "**\n\n")
	writeTable(b, []string{text.item, text.value}, [][]string{
		{text.site, opts.Config.SiteID},
		{text.ownDomain, opts.Config.OwnDomain + boolSuffix(text, opts.Config.HasSubdomains())},
		{text.competitors, join(text, opts.Config.Competitors)},
		{text.brandTerms, join(text, opts.Config.BrandTerms)},
		{text.market, opts.Config.Market},
	})
	b.WriteString("\n")
	if len(opts.Config.BrandTerms) == 0 {
		b.WriteString(text.brandTermsMissing + "\n\n")
	}
}

func writeObserved(b *strings.Builder, text labels, result *analysis.Result) {
	b.WriteString("## " + text.observedH + "\n\n")
	b.WriteString(text.observedNote + "\n\n")
	writeTable(b, []string{text.item, text.count}, [][]string{
		{text.run, fmt.Sprintf("%d", result.Totals.RunsTotal)},
		{text.success, fmt.Sprintf("%d", result.Totals.RunsSuccess)},
		{text.failure, fmt.Sprintf("%d", result.Totals.RunsError)},
		{text.cancelled, fmt.Sprintf("%d", result.Totals.RunsCancelled)},
		{text.unknownRuns, fmt.Sprintf("%d", result.Totals.RunsUnknown)},
	})
	b.WriteString("\n")

	rows := make([][]string, 0, len(result.Groups))
	for _, group := range result.Groups {
		rows = append(rows, []string{
			group.Provider, group.Model, zeroAsDash(group.Stage),
			fmt.Sprintf("%d", group.RunsTotal),
			fmt.Sprintf("%d", group.RunsSuccess),
			fmt.Sprintf("%d", group.RunsError),
			fmt.Sprintf("%d", group.RunsCancelled),
			fmt.Sprintf("%d", group.RunsUnknown),
		})
	}
	if len(rows) > 0 {
		writeTable(b, []string{text.provider, text.model, text.stage, text.run, text.success, text.failure, text.cancelled, text.unknown}, rows)
	} else {
		b.WriteString(text.noRuns + "\n")
	}
	b.WriteString("\n")
}

func writeDerived(b *strings.Builder, text labels, lang i18n.Lang, result *analysis.Result, opts Options) {
	b.WriteString("## " + text.derivedH + "\n\n")
	b.WriteString(text.derivedNote + "\n\n")

	b.WriteString("### " + text.geoH + "\n\n")
	if len(result.Groups) == 0 {
		b.WriteString("`" + lang.Unmeasured() + "`\n\n")
	} else {
		rows := make([][]string, 0, len(result.Groups))
		for _, group := range result.Groups {
			rows = append(rows, []string{
				group.Provider, group.Model, zeroAsDash(group.Stage),
				group.CitedOwn.Display(lang), group.BrandMentioned.Display(lang),
			})
		}
		writeTable(b, []string{text.provider, text.model, text.stage, text.citedRate, text.brandRate}, rows)
		b.WriteString("\n" + text.brandNote + "\n\n")
	}

	b.WriteString("### " + text.aeoQH + "\n\n")
	if len(result.Groups) == 0 {
		b.WriteString("`" + lang.Unmeasured() + "`\n\n")
	}
	for _, group := range result.Groups {
		fmt.Fprintf(b, "#### %s / %s / %s\n\n", group.Provider, group.Model, zeroAsDash(group.Stage))
		if len(group.PerQuestion) == 0 {
			b.WriteString("`" + lang.Unmeasured() + "`\n\n")
			continue
		}
		rows := make([][]string, 0, len(group.PerQuestion))
		for _, row := range group.PerQuestion {
			rows = append(rows, []string{row.QuestionKey, zeroAsDash(row.Category), row.QuestionText, row.Rate.Display(lang)})
		}
		writeTable(b, []string{text.question, text.category, text.questionText, text.acquireRate}, rows)
		b.WriteString("\n")
	}

	b.WriteString("### " + text.coverageH + "\n\n")
	if len(result.Groups) == 0 {
		b.WriteString("`" + lang.Unmeasured() + "`\n\n")
	}
	for _, group := range result.Groups {
		fmt.Fprintf(b, "#### %s / %s / %s\n\n", group.Provider, group.Model, zeroAsDash(group.Stage))
		coverage := group.Coverage
		writeTable(b, []string{text.item, text.value}, [][]string{
			{text.coverageRate, coverage.Rate.Display(lang)},
			{text.denominator, text.denominatorValue},
			{text.coveredQs, joinOrNone(text, coverage.CoveredQuestions)},
			{text.unmeasuredQs, joinOrNone(text, coverage.UnmeasuredQuestions)},
			{text.unknownOnlyQs, joinOrNone(text, coverage.UnknownOnlyQuestions)},
		})
		b.WriteString("\n")
	}

	b.WriteString("### " + text.competitorH + "\n\n")
	b.WriteString(text.competitorNote + "\n\n")
	if len(result.Groups) == 0 {
		b.WriteString("`" + lang.Unmeasured() + "`\n\n")
	}
	for _, group := range result.Groups {
		fmt.Fprintf(b, "#### %s / %s / %s\n\n", group.Provider, group.Model, zeroAsDash(group.Stage))
		if len(group.CompetitorRate) == 0 {
			b.WriteString("`" + text.noCitation + "`\n\n")
			continue
		}
		rows := make([][]string, 0, len(group.CompetitorRate))
		for _, row := range group.CompetitorRate {
			registered := text.notRegistered
			if row.Registered {
				registered = text.registered
			}
			rows = append(rows, []string{row.Domain, classification(text, row.Classification), registered, row.Rate.Display(lang)})
		}
		writeTable(b, []string{text.domain, text.classification, text.registeredCol, text.runRate}, rows)
		b.WriteString("\n")
	}

	b.WriteString("### " + text.shareH + "\n\n")
	b.WriteString(text.shareNote + "\n\n")
	if len(result.Groups) == 0 {
		b.WriteString("`" + lang.Unmeasured() + "`\n\n")
	}
	for _, group := range result.Groups {
		fmt.Fprintf(b, "#### %s / %s / %s (%d citations)\n\n", group.Provider, group.Model, zeroAsDash(group.Stage), group.CitationTotal)
		if len(group.DomainShare) == 0 {
			b.WriteString("`" + text.noCitation + "`\n\n")
			continue
		}
		rows := make([][]string, 0, len(group.DomainShare))
		for _, row := range group.DomainShare {
			rows = append(rows, []string{row.Domain, classification(text, row.Classification), fmt.Sprintf("%d", row.Count), row.Share.Display(lang)})
		}
		writeTable(b, []string{text.domain, text.classification, text.citationCount, text.share}, rows)
		b.WriteString("\n")
	}
	_ = opts
}

func writeCompare(b *strings.Builder, text labels, lang i18n.Lang, result *analysis.Result, opts Options) {
	other := opts.Compare
	b.WriteString("## " + text.compareH + "\n\n")
	fmt.Fprintf(b, text.compareNote+"\n\n", other.Period.Label, result.Period.Label)
	rows := make([][]string, 0, len(result.Groups))
	for _, row := range analysis.Compare(other, result) {
		rows = append(rows, []string{
			row.Provider, row.Model, zeroAsDash(row.Stage),
			row.CitedA.Display(lang), row.CitedB.Display(lang), row.CitedDeltaText,
			row.BrandA.Display(lang), row.BrandB.Display(lang), row.BrandDeltaText,
			row.NoteFor(lang),
		})
	}
	if len(rows) == 0 {
		b.WriteString("`" + text.noComparable + "`\n\n")
		return
	}
	writeTable(b, []string{
		text.provider, text.model, text.stage,
		text.citedBefore, text.citedAfter, text.delta,
		text.brandBefore, text.brandAfter, text.delta, text.note,
	}, rows)
	b.WriteString("\n")
}

func writeTrends(b *strings.Builder, text labels, lang i18n.Lang, series []analysis.TrendSeries, interval string) {
	fmt.Fprintf(b, "## "+text.trendsH+" (%s)\n\n", interval)
	b.WriteString(text.trendsNote + "\n\n")
	for _, one := range series {
		fmt.Fprintf(b, "### %s / %s / %s\n\n", one.Provider, one.Model, zeroAsDash(one.Stage))
		rows := make([][]string, 0, len(one.Points))
		for _, point := range one.Points {
			rows = append(rows, []string{
				point.Bucket,
				fmt.Sprintf("%d", point.RunsTotal),
				fmt.Sprintf("%d", point.RunsValid),
				fmt.Sprintf("%d", point.RunsError),
				fmt.Sprintf("%d", point.RunsUnknown),
				point.CitedOwn.Display(lang),
				point.CitedDeltaText,
				point.BrandMentioned.Display(lang),
				point.NoteFor(lang),
			})
		}
		writeTable(b, []string{text.bucket, text.run, text.success, text.failure, text.unknown, text.citedRate, text.delta, text.brandRate, text.note}, rows)
		b.WriteString("\n")
	}
}

func writeLimits(b *strings.Builder, text labels) {
	b.WriteString("## " + text.limitsH + "\n\n")
	for _, line := range text.limits {
		b.WriteString("- " + line + "\n")
	}
}

func writeTable(b *strings.Builder, header []string, rows [][]string) {
	b.WriteString("| " + strings.Join(header, " | ") + " |\n")
	b.WriteString("|" + strings.Repeat("---|", len(header)) + "\n")
	for _, row := range rows {
		b.WriteString("| " + strings.Join(row, " | ") + " |\n")
	}
}

func join(text labels, values []string) string {
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			cleaned = append(cleaned, value)
		}
	}
	if len(cleaned) == 0 {
		return text.notMeasured
	}
	return strings.Join(cleaned, ", ")
}

func joinOrNone(text labels, values []string) string {
	if len(values) == 0 {
		return text.none
	}
	return strings.Join(values, ", ")
}

func zeroAsDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}

func shortHashes(text labels, values []string) string {
	if len(values) == 0 {
		return text.notMeasured
	}
	short := make([]string, 0, len(values))
	for _, value := range values {
		if len(value) > 12 {
			value = value[:12] + "..."
		}
		short = append(short, value)
	}
	return strings.Join(short, ", ")
}

func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func boolSuffix(text labels, subdomains bool) string {
	if subdomains {
		return text.subdomainsIncluded
	}
	return text.exactOnly
}

func classification(text labels, value string) string {
	switch value {
	case "own":
		return text.classOwn
	case "competitor":
		return text.classCompetitor
	case "unknown", "other (malformed url)":
		return text.classUnknown
	default:
		return text.classOther
	}
}
