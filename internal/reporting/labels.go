package reporting

import "github.com/routeflags/ai-citation-monitor/internal/i18n"

// labels is every string the report needs. Keeping them in one place makes it
// impossible to add a section in one language only.
type labels struct {
	title, generated                                                                                       string
	conditionsH, conditionsNote, item, value                                                               string
	mixedConditions                                                                                        string
	provider, model, stage, endpoint, search, method, qsVersion, qsSha, normVersion, observedRange         string
	brandTermsStatus, brandTermsStatusProposed, brandTermsStatusConfirmed, brandTermsStatusNotDeclared     string
	brandTermsStatusNote                                                                                   string
	brandTermsProposedCaution                                                                              string
	configH, site, ownDomain, competitors, brandTerms, market, subdomainsIncluded, exactOnly               string
	brandTermsMissing                                                                                      string
	observedH, observedNote, run, success, failure, cancelled, unknown, unknownRuns, count, noRuns         string
	derivedH, derivedNote                                                                                  string
	geoH, citedRate, brandRate, brandNote                                                                  string
	aeoQH, question, category, questionText, acquireRate                                                   string
	coverageH, coverageRate, denominator, denominatorValue, coveredQs, unmeasuredQs, unknownOnlyQs         string
	competitorH, competitorNote, domain, classification, registeredCol, registered, notRegistered, runRate string
	shareH, shareNote, citationCount, share, noCitation                                                    string
	compareH, compareNote, citedBefore, citedAfter, brandBefore, brandAfter, delta, note, noComparable     string
	trendsH, trendsNote, bucket                                                                            string
	limitsH                                                                                                string
	limits                                                                                                 []string
	notMeasured, none                                                                                      string
	classOwn, classCompetitor, classOther, classUnknown                                                    string
}

var enLabels = labels{
	title:       "AI citation report (%s)",
	generated:   "Generated: %s (UTC). Period boundaries are JST (%s inclusive to %s exclusive)",
	conditionsH: "Conditions",
	conditionsNote: "**Conditions read from the records.** This is where the numbers come " +
		"from. A group is provider x model x stage, so records with different conditions " +
		"can meet in it; such a group lists the fields that differ (`mixed_conditions`) " +
		"and its rate is not one measurement.",
	mixedConditions: "**%s** mixes measurement conditions (%s). Its rate is an average " +
		"over them, not one measurement.",
	item: "item", value: "value",
	provider: "provider", model: "model", stage: "stage",
	endpoint: "API endpoint version", search: "search enabled", method: "measurement method",
	qsVersion: "question set version", qsSha: "question set sha256", normVersion: "normaliser version",
	observedRange:               "observed range (UTC)",
	brandTermsStatus:            "brand terms status (question set)",
	brandTermsStatusProposed:    "proposed (not confirmed)",
	brandTermsStatusConfirmed:   "confirmed",
	brandTermsStatusNotDeclared: "not declared",
	brandTermsStatusNote: "The status is what the imported question set declared about its own `brand_terms`. " +
		"The terms actually matched come from the configuration, so the status says how settled the term " +
		"list is, not which words were used.",
	brandTermsProposedCaution: "**The brand terms are not confirmed yet (proposed).** A mention rate depends " +
		"on how the terms are chosen, so read the rate as a value taken before the list was confirmed.",
	configH: "Configuration used for aggregation (domain matching and labels)",
	site:    "site", ownDomain: "own domain", competitors: "competitor domains",
	brandTerms: "brand terms", market: "market",
	subdomainsIncluded: " (subdomains included)", exactOnly: " (exact match only)",
	brandTermsMissing: "No brand terms are configured, so the brand mention rate is `— (not measured)`.",
	observedH:         "Observed",
	observedNote: "Counts before aggregation. Failures and unknown runs are excluded from the " +
		"denominators but still counted here. Status and citation observability are separate " +
		"dimensions: a failed run has no observed citation metadata either, so it appears in " +
		"both `failure` and `unknown`.",
	run: "runs", success: "success", failure: "failure", cancelled: "cancelled",
	unknown: "unknown", unknownRuns: "citation metadata not observed (unknown)", count: "count",
	noRuns:   "No runs in this period (`— (not measured)`).",
	derivedH: "Derived",
	derivedNote: "The denominator of a citation rate is \"runs that succeeded and returned citation " +
		"metadata\". n is the denominator. `— (not measured)` is not 0%. Totals across providers, " +
		"models or stages are never produced.",
	geoH:      "GEO: citation rate and brand mention rate",
	citedRate: "own citation rate", brandRate: "brand mention rate",
	brandNote: "Brand terms are normalised (NFKC, whitespace folded, lower case); an " +
		"ASCII term must sit on word boundaries, so a hit inside a longer word is " +
		"counted as ambiguous and left out of the rate. A generic brand term can " +
		"still match a sentence about someone else.",
	aeoQH: "AEO: citation rate per question", question: "question", category: "category",
	questionText: "question text", acquireRate: "citation rate",
	coverageH: "AEO: question coverage", coverageRate: "coverage",
	denominator:      "denominator",
	denominatorValue: "questions with at least one citation-judgeable answer",
	coveredQs:        "questions with an own citation", unmeasuredQs: "questions not measured",
	unknownOnlyQs: "questions with unknown answers only",
	competitorH:   "GEO: citation rate per domain",
	competitorNote: "The denominator is the valid runs of the same group. One answer can cite " +
		"several domains, so the column does not add up to 100%.",
	domain: "domain", classification: "class", registeredCol: "registered",
	registered: "registered", notRegistered: "observed only", runRate: "citation rate",
	shareH: "GEO: citation share",
	shareNote: "The denominator is the total number of citations in the group (a URL without a " +
		"host still counts as one). The scope of the denominator is printed per group.",
	citationCount: "citations", share: "share", noCitation: "— (no citation)",
	compareH: "Period comparison",
	compareNote: "`%s` -> `%s`. Differences are in percentage points. Rows whose conditions " +
		"differ do not get a delta.",
	citedBefore: "citation (before)", citedAfter: "citation (after)",
	brandBefore: "mention (before)", brandAfter: "mention (after)",
	delta: "delta", note: "note", noComparable: "— (no comparable runs)",
	trendsH: "Trend",
	trendsNote: "Buckets are JST days. Differences are in percentage points. The first bucket " +
		"has nothing to compare with, so it says so.",
	bucket:  "bucket",
	limitsH: "What this report does not say",
	limits: []string{
		"Causality (\"citations grew because of X\") or interpretation. The numbers are observation and derivation only.",
		"Totals across providers, models or stages (the denominator stays inside a group).",
		"Answer correctness against a ground truth (out of scope for this tool).",
		"Values that treat unknown (citation metadata was not observed) as zero citations.",
		"The consumer ChatGPT / Gemini UI. This is a sample observed through the official API.",
	},
	notMeasured: "`— (not measured)`", none: "none",
	classOwn: "own", classCompetitor: "competitor", classOther: "other", classUnknown: "unknown (malformed URL)",
}

var jaLabels = labels{
	title:       "AI 引用計測レポート（%s）",
	generated:   "生成: %s（UTC）。期間の区切りは JST（%s 以上 %s 未満）",
	conditionsH: "条件",
	conditionsNote: "**記録から読んだ条件。** ここが数字の出所。グループは provider × model × " +
		"stage なので、条件の違う記録が同じグループに入り得る。その場合は混在した項目" +
		"（`mixed_conditions`）を明示し、その率は 1 つの測定ではない。",
	mixedConditions: "**%s** は計測条件が混在している（%s）。率はそれらの平均であり、" +
		"1 つの測定ではない。",
	item: "項目", value: "値",
	provider: "プロバイダー", model: "モデル", stage: "stage",
	endpoint: "API エンドポイント版", search: "検索の有効化", method: "測定方法",
	qsVersion: "質問セット版", qsSha: "質問セット sha256", normVersion: "正規化仕様の版",
	observedRange:               "観測の範囲（UTC）",
	brandTermsStatus:            "ブランド語の状態（質問セット）",
	brandTermsStatusProposed:    "未確定（proposed）",
	brandTermsStatusConfirmed:   "確定済み",
	brandTermsStatusNotDeclared: "未宣言",
	brandTermsStatusNote: "状態は、取り込んだ質問セットが自身の `brand_terms` について宣言したもの。" +
		"実際に照合に使う語は設定側のブランド語なので、状態は語の確定度を表し、どの語を使ったかは表さない。",
	brandTermsProposedCaution: "**ブランド語はまだ確定していない（proposed）。** 言及率は語の選び方に" +
		"依存するため、確定前の値として読むこと。",
	configH: "集計に使った設定（ドメイン判定とラベルに使う）",
	site:    "サイト", ownDomain: "自社ドメイン", competitors: "競合ドメイン",
	brandTerms: "ブランド語", market: "市場",
	subdomainsIncluded: "（サブドメインを含む）", exactOnly: "（完全一致のみ）",
	brandTermsMissing: "ブランド語が未設定のため、ブランド言及率は `—（未測定）` になる。",
	observedH:         "観測（OBSERVED）",
	observedNote: "集計前の件数。失敗と unknown は分母から外すが、件数としてはここに出る。" +
		"状態（status）と引用の観測（observability）は別の次元で、失敗した run も引用メタデータは" +
		"観測されていないため、`failure` と `unknown` の両方に出る。",
	run: "run", success: "成功", failure: "失敗", cancelled: "中断",
	unknown: "unknown", unknownRuns: "引用メタデータ未観測（unknown）", count: "件数",
	noRuns:   "この期間に run が無い（`—（未測定）`）。",
	derivedH: "導出（DERIVED）",
	derivedNote: "率の分母は「成功し、かつ引用メタデータが返った run」。n は分母。`—（未測定）` " +
		"は 0% ではない。プロバイダー・モデル・stage をまたいだ合計は出さない。",
	geoH:      "GEO: 引用率・ブランド言及率",
	citedRate: "自社引用率", brandRate: "ブランド言及率",
	brandNote: "ブランド語は正規化（NFKC・空白畳み込み・小文字化）して照合する。ASCII の語は" +
		"語境界で一致させるので、長い語の内側にしか現れない一致は曖昧（ambiguous）として率から外す。" +
		"屋号のような一般語は、他社を指す文でも一致し得る。",
	aeoQH: "AEO: 質問別 引用獲得率", question: "質問", category: "分類",
	questionText: "質問文", acquireRate: "獲得率",
	coverageH: "AEO: 質問カバー率", coverageRate: "カバー率",
	denominator:      "分母",
	denominatorValue: "引用判定が可能な回答が 1 件以上ある質問",
	coveredQs:        "自社引用があった質問", unmeasuredQs: "未計測の質問",
	unknownOnlyQs: "unknown のみの質問",
	competitorH:   "GEO: ドメイン別 引用獲得率",
	competitorNote: "分母は同じグループの有効 run。1 つの回答が複数ドメインを引用するので、" +
		"合計は 100% を超え得る。",
	domain: "ドメイン", classification: "区分", registeredCol: "登録",
	registered: "登録済み", notRegistered: "観測のみ", runRate: "引用獲得率",
	shareH: "GEO: 引用シェア",
	shareNote: "分母はそのグループの引用総件数（ホストが取れない URL も 1 件として数える）。" +
		"分母のスコープはグループ内で閉じている。",
	citationCount: "引用件数", share: "シェア", noCitation: "—（引用なし）",
	compareH:    "期間比較",
	compareNote: "`%s` → `%s`。差は percentage points。条件が違う行は差を出さない。",
	citedBefore: "引用率（前）", citedAfter: "引用率（後）",
	brandBefore: "言及率（前）", brandAfter: "言及率（後）",
	delta: "差", note: "注記", noComparable: "—（比較できる run が無い）",
	trendsH: "トレンド",
	trendsNote: "バケットの区切りは JST。差は percentage points。最初のバケットは比較相手が" +
		"無いので「履歴不足」。",
	bucket:  "バケット",
	limitsH: "このレポートが書かないこと",
	limits: []string{
		"因果（「引用が増えたのは○○のため」）と解釈。数値は観測と導出だけ。",
		"プロバイダー・モデル・stage をまたいだ合計値（分母はグループで閉じる）。",
		"回答の正確さ（正解データとの照合が必要で、このツールの範囲外）。",
		"unknown（引用メタデータが観測できなかった run）を「引用ゼロ」として扱った値。",
		"一般向け ChatGPT / Gemini 画面の値。API で観測した標本に限る。",
	},
	notMeasured: "`—（未測定）`", none: "なし",
	classOwn: "自社", classCompetitor: "競合", classOther: "その他", classUnknown: "不明（URL 不正）",
}

func labelsFor(lang i18n.Lang) labels {
	if lang == i18n.JA {
		return jaLabels
	}
	return enLabels
}
