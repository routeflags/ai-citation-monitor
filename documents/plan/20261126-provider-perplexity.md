# Perplexity Agent API プロバイダー追加 計画書

| 項目 | 内容 |
|---|---|
| 日付 | 2026-11-26 |
| 対象 | `ai-citation-monitor` `main` = `7661bff` |
| 入力 | オーナー依頼「Perplexity — Agent API」（Grok/xAI は別スレッド） |
| 制約 | 実 API を叩かない／鍵を読まない・保存しない／既存 3 プロバイダーの挙動と記録形式を変えない／費用機能を足さない／push・merge はしない |
| 記録先 | 決定記録 D17（Perplexity） |
| 前提 | `documents/plan/20261115-phase1-deltas.md` T4（Anthropic 追加）と同じ粒度・同じ規約 |
| 範囲 | **Perplexity のみ**（Grok/xAI は別スレッドが担当するため、本計画・実装・ドキュメントには含めない） |

> 2026-11-28 の実 API 観測で、`annotations` は空で `search_results` が引用の実体であることを確認した。
> 現在の規則は決定記録 D17 の付記を参照（本計画は実装時点の記録）。

BDD（振る舞い単位）で分解する。各タスクは「入力 → 期待する振る舞い」と異常系を持ち、実装層
（アダプター／設定／CLI／ドキュメント）ごとにシナリオを整理する。

---

## 0. 調査で確定した公式仕様（確認日 2026-11-26）

出典: <https://docs.perplexity.ai/api-reference/agent-post>（OpenAPI）、
<https://docs.perplexity.ai/docs/agent-api/tools/web-search>（Response Shape）、
<https://docs.perplexity.ai/docs/agent-api/quickstart>。

| 項目 | 確認した内容 |
|---|---|
| エンドポイント | `POST https://api.perplexity.ai/v1/agent`（`/v1/responses` は OpenAI 互換エイリアス） |
| 認証 | `Authorization: Bearer $PERPLEXITY_API_KEY` |
| リクエスト | `{"model", "input", "tools": [{"type": "web_search"}]}`。`stream` 既定 false |
| 応答 | `{id, object:"response", created_at, status, model, output[], usage, error}`（OpenAI Responses 風） |
| `status` | `completed` / `failed` / `incomplete` / `in_progress` / `queued` / `cancelled` |
| 回答本文 | `output[]` の `type="message"` → `content[]` の `type="output_text"` の `text` |
| 引用 | `search_results` アイテムの `results[]`（`id` / `url` / `title`）と、`output[].content[].annotations[]`（`type="url_citation"`, `url` / `title` / `start_index` / `end_index`）。同じ URL は 1 件 |
| 検索結果 | `output[]` の `type="search_results"`（`queries[]`, `results[]{id,url,title,snippet,date,last_updated,source}`）。公式ドキュメントはこの `id`/`url` を引用の正としている。トップレベル `results[]` はクイックスタートの例にだけ現れ、OpenAPI の応答スキーマには無い |
| 検索の実行証跡 | `search_results` 出力アイテム、または `content[].annotations` の存在 |
| usage | `input_tokens` / `output_tokens` / `total_tokens`（すべて required）、`tool_calls_details.<tool>.invocation`（検索は `search_web` キーで報告）、`cost` / `cost_usd` |
| エラー | `{"error": {code, message, type}}`（400 は明記。401/429 の本文形は未明記） |
| モデル | **マルチプロバイダー**（`openai/gpt-5.6-sol` など）。`provider` 列は API 面を指す |

**注意（重要）**: web_search のドキュメントは「マーカーの有無にかかわらず
`search_results` の `id`/`url` を引用の正とせよ」と明記している。`annotations` は
OpenAPI にあるが任意（モデルがインラインマーカーを付けたとき）なので、片方だけを
読むと実運用で引用を取りこぼす。本ツールは **両方を併合し、同じ URL は 1 件**とし、
本文は解析しない（不変条件 4）。決定 D17 に明記。

---

## 1. 決定事項（要約。詳細は D17）

| 論点 | 決定 |
|---|---|
| provider id | `perplexity` |
| 既定 base_url / パス | `https://api.perplexity.ai/v1` + `/agent` |
| 既定 env | `PERPLEXITY_API_KEY` |
| `api_endpoint_version` | `agent/v1+web_search` |
| 引用の正 | `search_results.results[]` ＋ `annotations[]` の `url_citation`（同じ URL は 1 件） |
| トップレベル `results[]` | 読まない（OpenAPI のスキーマ外。`search_results` が正式な位置） |
| observability | `annotations` フィールドの存在（空配列含む）または `search_results` アイテム → `observed`。どちらも無い → `unknown` |
| 空回答 | 本文も引用も空 → `error_code=empty_answer` |
| status | `failed` / `incomplete` / `in_progress` / `queued` / `cancelled` は成功扱いにしない |
| tool_calls | `usage.tool_calls_details.search_web.invocation`、無ければ `search_results` アイテム数 |
| total_tokens | 返れば記録、無ければ nil（捏造しない） |
| 費用 | 計算しない（D11 維持）。`cost` 系フィールドは読まない |
| ライブ観測 | `LiveObservation` に**追加しない**（実 API 未観測） |

---

## 2. タスク（BDD）

### T1. `internal/providers/perplexity` アダプター

| # | 入力（fixture / 状況） | 期待する振る舞い |
|---|---|---|
| T1-1 | `search_results.results[]` 2 件と同じ URL を含む `annotations[]`（1 件は重複、1 件は追加 URL） | success、`observability=observed`、引用は **3 件**（URL ごとに 1 件）、`title` / `start_index` / `end_index` を保持 |
| T1-2 | 検索は走ったが `results: []` かつ `annotations: []` | success、`observed`、引用 **0 件**（`unknown` にしない） |
| T1-3 | `search_results` アイテムだけがあり `annotations` フィールドが無い | success、`observed`、引用は `results[]` の件数、`tool_calls` は可視の検索アイテム数 |
| T1-3b | `search_results` が無く `annotations` だけ | success、`observed`、引用は annotations の URL |
| T1-4 | `annotations` も `search_results` も無い（本文のみ） | success、`observability=unknown`、`tool_calls=0` |
| T1-5 | 401（`{"error":{code,message,type}}`） | `error` / `error_kind=provider`、code はプロバイダーの値、鍵が redact、リトライしない |
| T1-6 | エラー本文に鍵がそのまま入る（403） | `error_message` で `[redacted]` に置換される |
| T1-7 | 429 → 200 | `httpclient` のリトライに乗る（2 回目で成功、最終記録は success） |
| T1-8 | 本文も引用も空（`status=completed`） | `error_code=empty_answer` |
| T1-9 | `status=failed` | `error`、code は `error.code`（無ければ `failed`） |
| T1-10 | `status=incomplete` / `in_progress` / `queued` / `cancelled` | それぞれ `incomplete` / `in_progress` / `queued` / `cancelled_by_provider` のエラーコードで失敗扱い |
| T1-11 | usage あり | `input_tokens` / `output_tokens` / `total_tokens` を記録、`tool_calls` は `tool_calls_details.search_web.invocation`（`web_search` キーも許容） |
| T1-12 | usage の `total_tokens` 欠落 | `TotalTokens=nil`（0 で埋めない） |
| T1-13 | リクエスト検査 | `POST {base}/agent`、`Authorization: Bearer`、`model`/`input`/`tools[0].type=web_search`、`stream` を送らない |
| T1-14 | 200 で非 JSON / 未知のエラー封筒（500 + HTML） | `response_not_json` / `http_500` に落ち、本文先頭を保持 |
| T1-15 | `EndpointVersion()` / `Name()` | `agent/v1+web_search` / `perplexity` |

### T2. 設定（`internal/config`）

| # | 入力 | 期待する振る舞い |
|---|---|---|
| T2-1 | 設定に `[providers.perplexity]` | `Provider("perplexity")` で読める |
| T2-2 | 未設定 | `api_key_env` 既定 `PERPLEXITY_API_KEY`、`base_url` 既定 `https://api.perplexity.ai/v1`、`timeout_seconds` 90 |
| T2-3 | `model` 未設定 | 既定値を入れない（`aicm run` は既存どおり設定エラー exit 2） |
| T2-4 | `ProviderNames()` | `openai, gemini, anthropic, perplexity`（既存順の末尾に append） |
| T2-5 | `aicm init` が設定を保存 | `[providers.perplexity]` ブロックが書かれ、鍵の値は書かれない |
| T2-6 | 未知プロバイダー指定 | 既存どおり `unknown provider %q (this build supports: ...)` に 4 件が並ぶ |

### T3. CLI 配線（`internal/cli`）

| # | 入力 | 期待する振る舞い |
|---|---|---|
| T3-1 | `aicm run --provider perplexity --repeat 1 --yes` | `/agent` にだけ 2 回（2 質問）呼ばれ、他プロバイダーは 0 回 |
| T3-2 | 同上の記録 | `runs.provider='perplexity'`、`api_endpoint_version='agent/v1+web_search'`、`cited_own=1`・`citation_count=2` |
| T3-3 | `aicm run --provider openai,perplexity` | 既存の複数プロバイダー同時実行に乗り、グループが混ざらない |
| T3-4 | `aicm version`（人間向け） | endpoints 行に 4 プロバイダー。ライブ観測の行が「未観測のプロバイダー」を明示し、新アダプターを観測済みと誤読させない |
| T3-5 | `aicm version --json` | `providers` に 4 件、`endpoints` に新 1 件、`live_observations` は 3 件のまま、`live_not_observed` に `perplexity` |
| T3-6 | `internal/cli/forbidden_test.go` | `net/http` 使用パッケージ一覧に `perplexity` が入る |

### T4. ドキュメント・記録

| # | 対象 | 期待する振る舞い |
|---|---|---|
| T4-1 | `docs/providers.md` | Perplexity の節（endpoint・auth・引用の正・observability・usage・endpoint version・マルチプロバイダー性・注意）と、ライブ状態バナーが「Perplexity は未観測」と読める。「Adding a provider」チェックリストに CLI / ドキュメント更新を含む |
| T4-2 | `docs/architecture.md` | パッケージ一覧に `perplexity` |
| T4-3 | `docs/cli-reference.md` | `--provider` の列挙、`aicm version` の出力例・説明 |
| T4-4 | `examples/config.toml` / `.env.example` | 新ブロックと変数名（値は空） |
| T4-5 | `README.md` / `README.ja.md` | 対応表に 1 行（「実装済み・実 API 未検証」）、スコープ文の「Perplexity アダプターは対象外」を削除 |
| T4-6 | `documents/decisions/20261126-provider-perplexity.md` | D17（確認日・URL・引用の正・endpoint version・observability・未検証） |

---

## 3. 触るファイル

**新規**

- `internal/providers/perplexity/{perplexity.go,perplexity_test.go,testdata/*.json}`
- `documents/plan/20261126-provider-perplexity.md`（本ファイル）
- `documents/decisions/20261126-provider-perplexity.md`

**更新**

- `internal/config/config.go`（`Providers` / `Provider()` / `ProviderNames()` / `ApplyDefaults` / `Save`）
- `internal/cli/run.go`（`buildProviders` の switch）
- `internal/cli/init.go`（`versionCommand` の endpoints と表示行、未観測リスト）
- `internal/cli/forbidden_test.go`（`net/http` 使用パッケージ一覧）
- `internal/cli/cli_test.go`（ハーネスに Perplexity の fixture とルーティング、e2e 1 本）
- `examples/config.toml`、`.env.example`
- `docs/{providers,architecture,cli-reference}.md`、`README.md`、`README.ja.md`

**触らない**

- `internal/providers/provider.go`（`LiveObservation` に追加しない、インターフェース変更なし）
- `internal/domain`（`NormalizerVersion` は据え置き。ドメイン判定を変えないため）
- 既存 3 アダプターと既存テストの期待値

---

## 4. 異常系・境界の一覧（横断）

1. 設定エラー（`model` 未設定 / unknown provider）→ exit 2、既存メッセージ語彙を維持。
2. 認証エラー（401/403）→ provider エラーとして**記録**し、分母から外す（不変条件 5）。鍵は `providers.Sanitize`。
3. 429/5xx → `httpclient` のリトライのみ。アダプターは自前リトライをしない。
4. 未完了（`incomplete` / `in_progress` / `queued` / `cancelled`）→ 成功扱いにしない。
5. 本文も引用も空 → `empty_answer`。
6. `search_results` も `annotations` も無い → 引用 0 件・`unknown`。どちらかがあれば `observed`。
7. 非 JSON / 未知のエラー封筒 → 落ちずに `http_<status>` もしくは `response_not_json` で記録。
8. 時刻は `Options.Now` のみ（アダプター内で `time.Now` を呼ばない）。
9. 1 呼び出し = 1 run。`stream` を送らず、`background` を使わない。

---

## 5. 未検証事項（実装後に残るもの）

- **実 API 未観測**。fixture（公式ドキュメントの形状を写したもの）でのみ検証する。`providers.LiveObservation` には追加しない。
- `search_results` が実運用で常に返るかは未検証。返らない場合は `unknown`（0 件ではない）。`annotations` は任意で、付かなくても `search_results` から引用を取る。実 API 観測時に確認する。
- 検索ツール使用量キー（`tool_calls_details.search_web`）と、401/429 のエラー本文形は未明記。アダプターは `search_web` / `web_search` の両キーを許容し、封筒が読めなければ `http_<status>` に落とす。
- トップレベル `results[]` は OpenAPI の応答スキーマに無い（クイックスタートの例には現れる）。現れても読まない（`search_results` が正式な位置）。
- Agent API はマルチプロバイダーなので、`provider` 列は「API 面」であり「モデル提供元」ではない。モデル別の比較は `model` 列で行う。

---

## 6. 進め方

1. アダプターと fixture・単体テスト。
2. 設定・CLI 配線・`forbidden_test`・CLI e2e。
3. ドキュメント（英語）と `README.ja.md`、決定記録 D17。
4. `gofmt -l .` / `go build ./...` / `go vet ./...` / `go test ./...` / `go test -race ./...` / `make check`。
5. レビュー観点: 不変条件（追記のみ／条件併記／グループ合計なし／率に n／n=0 は未測定／失敗も記録／秘密を書かない／費用なし）。既存 3 プロバイダーのテストが無変更で緑であること。
