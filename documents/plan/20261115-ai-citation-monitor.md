# ai-citation-monitor Phase 1 (MVP) 実装計画

| 項目 | 内容 |
|---|---|
| 日付 | 2026-11-15（改訂 2） |
| 版 | 2.0 |
| 入力 | `routeflags/ai-citation-monitor#1`（仕様 issue・正本）、`documents/20261115.md`（Go 開発指示書・正本。改変しない） |
| 決定 | `documents/decisions/20261115-phase1-decisions.md`（D1–D10。**D1 は承認済み**） |
| 参照 | `routeflags/seo-operations` `290d6ea` `scripts/geo/`（意味の参照のみ） |
| 成果物 | `aicm` 単一バイナリ（Go） |

## 0. 前提の変更

当初計画は「netloom 経由のみ」だったが、オーナー判断（2026-11-15）で **D1 は「ツール自身が
公式 API を `net/http` で直接叩く」に承認**された。理由は「独立した公開ツールであり、ほか
ツールへの依存ができない」。本計画はその前提で書き直している（差分は決定記録 D1）。

## 1. 調査（実コードと実物で確認した事実）

| 確認したこと | 手段 | 結果 |
|---|---|---|
| 抽出元の意味（正規化・重複畳み込み・error 扱い・率の分母・カバー率の分母・引用シェアの分母） | `scripts/geo/*.py` / `schema.sql` / `README.md` を読解 | 移植対象を確定。語彙（M2/M3 等）は持ち込まない |
| 実応答の形状 | `scripts/geo/fixtures/{openai,gemini,perplexity}-*.json` | アダプターのパース対象。テスト fixture として流用（推測でフィールドを作らない） |
| 実測 DB の形 | `sqlite3 /tmp/geo-aeo.db` | `runs` 8 件・`citations` 16 件。**カセット・応答はディスクに無い**（旧実装の証跡消失） |
| OpenAI の現行仕様 | 公式ドキュメント（Web search ガイド・Responses API リファレンス） | `POST /v1/responses`、`tools:[{"type":"web_search"}]`、引用は `output[].content[].annotations[]` の `url_citation`、`usage.input_tokens/output_tokens/total_tokens` |
| Gemini の現行仕様 | 公式ドキュメント（Grounding with Google Search・generateContent リファレンス） | `POST /v1beta/models/{model}:generateContent`、`tools:[{"google_search":{}}]`、引用は `candidates[].groundingMetadata.groundingChunks[].web.uri`、`usageMetadata.*TokenCount` |
| Go 環境 | `go version` | `go1.26.5 darwin/arm64`。`modernc.org/sqlite` / `BurntSushi/toml` / `cobra` / `yaml.v3` / `x/text` / `x/time` を使用 |

## 2. 受け入れ基準（issue の BDD 9 項目）と判定方法

| # | 基準 | この実装での読み替え | 固定するテスト |
|---|---|---|---|
| A1 計画 | 往復数を表示し、ネットワークを叩かない | `aicm run --dry-run` が 質問数×repeat を表示し、往復 0 | `TestRunDryRunDoesNotTouchTheNetwork` |
| A2 門 | 前提が揃わなければ exit 2 で理由、往復 0 | 設定不正 → exit 2 / 認証なし → exit 3（どちらも 1 回も送らない。設定と認証を別行で表示） | `TestConfigProblemsExit2`, `TestRunWithoutCredentialSendsNothing` |
| A3 追記性 | 同じ入力で 2 回目は `inserted=0` | 同じ条件・同じ session の run はスキップ（`plan_key` 一意） | `TestIngestIsAppendOnly`, `TestPlanSkipsAlreadyRecordedCalls` |
| A4 条件 | `model` 違いは別 run・別グループ | `(question_version, provider, model, repeat, stage, session)` が違えば別 run | `TestDifferentModelIsADifferentRun`, `TestDifferentModelIsADifferentGroup` |
| A5 分母 | 未測定は `0.0%` ではなく「—（未測定）」＋`n=0` | `n=0` の表示は言語別。英語 `— (not measured)` / 日本語 `—（未測定）` | `TestRateUnmeasuredIsNotZero`, `TestReportWithoutRunsSaysNotMeasured` |
| A6 分離 | 2 つの `source` でまたいだ合計率を出さない | グループ（provider×model×stage）ごとにのみ集計 | `TestBuildSeparatesProvidersAndModels`, `TestBuildNeverPrintsACrossGroupTotal` |
| A7 禁止依存 | provider SDK / HTTP クライアントの import 0 件 | **逆転**: provider SDK と `os/exec` が 0 件（HTTP は必須） | `TestNoProviderSDKIsImported`, `TestNoExternalMeasurementToolIsShelledOut`, `TestHTTPLayerIsUsedForTheProviders` |
| A8 証跡監査 | カセット・応答が無い run を一覧 | `aicm verify` が DB 内整合性（`responses` 欠落・参照切れ・ハッシュ不一致・空回答）を列挙し exit 1 | `TestIntegrityFindsMissingEvidence`, `TestVerifyFindsTamperedAnswer` |
| A9 オフライン | ネットワーク・認証なしで全経路が通る | `httptest` と fixture で init → import → run → report → export → verify | `TestFullPipelineRecordsReportsAndExports` |

## 3. タスク分解（振る舞い単位）

### T01 骨格・決定記録・配布物

- 振る舞い: `go build ./...` でビルドでき、決定記録・計画書・LICENSE・`.gitignore` が読める。
- 正常系: `cmd/aicm` / `internal/*` / `migrations/` / `examples/` / `docs/`。
- エッジ: 生成物（`*.db`、`reports/`、`measurements/`）は `.gitignore` で除外。実 API を叩くテストは無い。

### T02 設定（TOML）

- 振る舞い: 設定を読み、欠けた必須項目を**名前付きで**返す。秘密の**値**は読まない。
- 正常系: `site_id` / `own_domain` / `market` が揃えば実行可能。
- 異常系: `own_domain` に `/`・`:` → exit 2。
- エッジ: `api_key = "sk-..."` のような**値**が書かれていたら警告（値はエコーしない）。`[report] language` は既定 `en`。`max_retries = -1` でリトライ無効。

### T03 質問セットの取込（YAML / CSV）

- 振る舞い: 契約を検証してから DB に書く。不正なら 1 行も書かない。
- 正常系: 2 問の smoke/initial を読み、stage ごとの往復数を表示。
- 異常系: 語彙外 category / 重複 id / stage 参照切れ / `runs_per_question` が 1–5 外 / 正規化すると同一の質問文 → exit 1 で全件列挙。
- エッジ: `smoke ⊆ initial ⊆ regular` の崩れは警告（`--strict` は実装せず、CLI の `--check` で警告表示）。
- データ例: `Q01 "THCVとTHCの違いは？" / comparison`。

### T04 応答 → ドメイン判定（`internal/domain`）

- 振る舞い: URL を正規化し、自社判定・ブランド言及を決める。通信しない。
- 正常系: 4 件の引用 → 重複畳込みで 3 件、`mailto:` は malformed で残す。
- 異常系: 相対 URL・ホスト無し → malformed（捨てない）。
- エッジ: 末尾ドット・大文字ホスト・既定ポート・`notexample.com` は不一致。NFKC で全角を畳む。
- データ例: `https://www.thch-vape.shop/guide/thcv?utm_source=chatgpt#section` は同一ページ。

### T05 DB（migrations・追記のみ・`v_runs_valid`）

- 振る舞い: migration を適用し、run を INSERT のみで追記する。
- 正常系: 同じ `plan_key` は `inserted=false`、条件が違えば別 run。
- 異常系: 子行の参照切れは外部キーで拒否。
- エッジ: 既存 DB に再適用しても記録が消えない。トランザクションはスキップ時も必ず解放。
- データ例: 1 run + 1 response + 1 citation + 1 mention + 1 usage。

### T06 `run`（計画・認証・予算・実行・再開）

- 振る舞い: 1 質問 1 repeat = 1 往復。全試行を記録し、完了済みはスキップ。
- 正常系: 2 問 × 1 repeat = 2 往復、`success=2`。2 回目は `calls: 0 planned, 2 already recorded`。
- 異常系: 設定不正 exit 2 / 認証なし exit 3・往復 0 / 予算超過 exit 5・往復 0 / 全滅（transport）exit 4 / 一部失敗 exit 1。
- エッジ: Ctrl+C で新規投入を止め、完了分は記録済み。`--dry-run` はネットワーク 0。`--yes` なしの非 TTY は拒否（黙って課金しない）。
- データ例: `stage=smoke repeat=1 session=2026-11-15`。

### T07 集計（`metrics` / `compare` / `trends` / `competitors`）

- 振る舞い: 率の定義を 1 か所に固定し、グループ単位で返す。
- 正常系: `2/5` → `40.0% (n=2/5)`。
- 異常系: `n=0` → `— (not measured)`。条件違いの比較は差を出さず理由を出す。
- エッジ: unknown は分母から外し件数で出す。シェアの分母は malformed を含む。カバー率の分母は「判定可能な回答がある質問」。
- データ例: smoke 5 問 × 1、自社引用 0 → `0.0% (n=0/5)`、Q15 は `3/3` → `100.0%`。

### T08 レポート（ファイル名・固定文・言語）

- 振る舞い: Markdown を生成し、**ファイル名に全条件**（期間・provider・model・stage・質問セット sha）を含める。
- 正常系: `report-2026-11-12_2026-11-12-openai-gpt-6-luna-smoke-qs-2eee65141222.md`。
- 異常系: データ 0 件でも「— (not measured)」で書く。
- エッジ: 冒頭固定文は言語別（en/ja）、無効化不可。OBSERVED と DERIVED を分ける。

### T09 `export` / `verify`

- 振る舞い: DB → CSV/JSON/JSONL。DB 自身の整合性を監査。
- 正常系: csv はヘッダ + 1 行/run。`verify` は欠落 0 件で exit 0。
- 異常系: 回答改変・回答欠落 → 一覧して exit 1。
- エッジ: 大文字小文字を区別する SQL の特殊文字を CSV でクォート（encoding/csv）。

### T10 `init`

- 振る舞い: TOML とプロジェクト行を作る。秘密の値を書かない。
- 正常系: `--non-interactive` で完全な設定を生成。
- 異常系: 必須フラグ欠け exit 2 / 既存ファイル（`--force` なし）exit 2。
- エッジ: TTY でないときに対話を始めない。**明示的に渡したフラグだけ**が既存設定を上書きする。

### T11 費用・予算

- 振る舞い: 価格表から概算。価格が無ければ `unknown`（0 円扱いしない）。
- 異常系: 開始前の推定超過 → exit 5、往復 0。実行中の上限到達 → 残りを未実行として報告し exit 5。
- エッジ: トークン未申告は `unknown`、集計は下限値として扱う。

### T12 テスト（fixture と httptest）

- 振る舞い: ネットワーク・認証なしで全経路が通る。
- 正常系: `go test ./...` が green。
- 異常系: 禁止依存、追記性、カセット（廃止）→ DB 整合性、分母、分離、終了コードを個別に固定。
- エッジ: 偽サーバーは `httptest.NewServer`。実キーが必要なテストは 1 件も無い。

### T13 ドキュメント（英語既定）・CI・GoReleaser

- `README.md`（英語）+ `README.ja.md`（日本語、相互リンク）、`docs/{architecture,metrics,providers,cli-reference}.md`（英語）、`examples/`、`.github/workflows/ci.yml`、`.goreleaser.yaml`。
- 状態表は「未検証 / 検証済み」を機械的に区別し、実 API 未検証を検証済みと書かない。

## 4. 抽出時に直す不具合（回帰テスト付き）

| # | 不具合 | 直し方 | 回帰テスト |
|---|---|---|---|
| B1 | レポート名が stage を含まず上書きされる | 期間 + 全条件 + 質問セット sha をファイル名に含める | `TestFileNameCarriesEveryCondition` |
| B2 | `preflight` が認証を見ない | `run` と `--dry-run` が「設定が妥当」と「認証がある」を別に判定。認証欠落は exit 3 で往復 0 | `TestRunWithoutCredentialSendsNothing` |
| B3 | 証跡の消失を検出できない | 生応答を DB に保存し、`aicm verify` で DB 内整合性を監査 | `TestIntegrityFindsMissingEvidence` / `TestVerifyFindsTamperedAnswer` |
| B4 | README の状態が実測と食い違う | 状態表を「検証済み / 未検証」で分け、実 API 未検証を明記 | 目視（README） |

## 5. リスク

| リスク | 影響 | 対応 |
|---|---|---|
| 実 API のフィールド名が変わる | 抽出失敗 | 抽出をアダプターに隔離し、`api_endpoint_version` を記録。fixture を足すだけで追随できる。実 API 未検証であることを README と決定記録に明記 |
| 予算の厳密性 | 想定より使う | 事前推定＋実行中の上限で止める。厳密な保証ではないと出力に書く。`--yes` なしの非 TTY は送らない |
| 質問文の変更で比較不能 | 過去と比較できない | 質問版を本文の sha から導出し、`question_version` として run に焼き込む |
| ブランド語の一般名詞衝突 | 言及率が過大 | 部分一致であることをレポートに書き、3 文字未満は `ambiguous` |
| 撤回された netloom 前提の残骸 | 実装と文書の不一致 | 決定記録 D1 に破棄項目を列挙。`os/exec` と外部ツール名を機械検査で禁止 |
