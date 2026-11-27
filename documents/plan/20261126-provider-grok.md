# Grok (xAI) Responses API プロバイダー対応 計画書（T-G1–T-G7）

| 項目 | 内容 |
|---|---|
| 日付 | 2026-11-26 |
| 対象 | `ai-citation-monitor`（Go 製 CLI `aicm`）、`main` = `7661bff`（クリーン） |
| 入力 | オーナー依頼「Grok（xAI） Responses API への対応」 |
| 制約 | 実 API を叩かない／鍵を読まない／`providers.LiveObservation` に `grok` を足さない／既存 3 プロバイダーの挙動と記録形式を変えない／`NormalizerVersion` を変えない／費用機能を足さない（D11 維持）／Perplexity 側の成果物に触れない |
| 記録先 | 決定記録 D18（`documents/decisions/20261126-provider-grok.md`）。D17 は Perplexity が使用 |

> 2026-11-28 の実 API 観測で、`annotations` が位置 span を持たないことを確認し、span の無い注釈は
> 引用に数えない規則に変更した。詳細は決定記録 D18 の付記を参照（本計画は実装時点の記録）。

BDD（振る舞い単位）で分解する。各タスクは「入力 → 期待する振る舞い」と異常系を持ち、実装層ごと
にシナリオを整理する。参照した既存実装は `internal/providers/anthropic`（最も近い形：1 往復・
サーバーサイド検索・注釈ベースの引用・注入クロック・エラー封筒の先読み）と
`internal/providers/openai`（同じ Responses 系の `output[]` / `annotations[]` 構造）。

---

## 確認した公式仕様（2026-11-26 取得）

| 項目 | 確認した内容 | 出典 |
|---|---|---|
| エンドポイント | `POST {base_url}/responses`、既定 base URL `https://api.x.ai/v1` | REST reference（Responses） |
| 認証 | `Authorization: Bearer $XAI_API_KEY`。401 は「ヘッダなし／無効なトークン」 | REST reference / Debugging Errors |
| 入力 | `input` は文字列または role/content 配列。公式 cURL 例は配列（`{"role":"user","content":"..."}`） | REST reference / Web Search |
| 検索 | `tools: [{"type": "web_search"}]`。`tool_choice` を送らない（モデルが検索可否を決める） | Web Search |
| 引用 | `output[].content[].annotations[]` の `type = "url_citation"`（`url` / `start_index` / `end_index` / `title`、`title` は「1」「2」等の**引用番号**） | Citations（構造化インライン引用） |
| トップレベル `citations[]` | 「検索中に見た全 URL」で、回答が使った根拠とは限らない（公式が明記） | Citations（All Citations） |
| インラインリンク | 本文中の `[[N]](url)` は既定で有効。`include: ["no_inline_citations"]` を送ると注釈は「検索で見た URL の一覧」になり位置参照を持たない | Citations（Configuring Inline Citations） |
| 応答 | `output[]` に `web_search_call` と `message`（`content[].type = output_text` / `refusal`） | REST reference / Tool Usage Details |
| status | `completed` / `in_progress` / `incomplete`（`failed` は列挙に無い。失敗は `error` オブジェクト） | REST reference |
| `incomplete_details.reason` | `max_output_tokens` / `max_prompt_tokens` / `max_time_limit` / `content_filter` | OpenAPI `IncompleteDetails` |
| usage | `input_tokens` / `output_tokens` / `total_tokens` / `num_server_side_tools_used` / `server_side_tool_usage_details.web_search_calls` | REST reference |

取得元 URL:

- `https://docs.x.ai/developers/rest-api-reference/inference/responses`
- `https://docs.x.ai/developers/tools/web-search`
- `https://docs.x.ai/developers/tools/citations`
- `https://docs.x.ai/developers/tools/tool-usage-details`
- `https://docs.x.ai/developers/debugging`
- `https://docs.x.ai/openapi.json`（`Annotation` / `OutputMessageContent` / `WebSearchCall` / `IncompleteDetails`）

**出典の食い違い**: `citations` 一覧は Citations ページに記述があるが、Responses の応答スキーマ
（`ModelResponse`）には `citations` プロパティが無い。`include` の対応値も Citations ページの記述と
`ModelRequest.include` で一致しない。どちらも「送らない／読まない」側に倒してあり、その旨を D18 に
残す。

**確認できなかったこと**: エラー本文の JSON 封筒。REST reference の 4xx は説明文のみでスキーマが無く、
OpenAPI にも 4xx の schema が無い（2026-11-26 時点）。そこで D18 のとおり**2 形態を両受け**する。

---

## T-G1. アダプター `internal/providers/grok`

`providers.Provider`（`Name` / `EndpointVersion` / `Ask`）を実装する。`Options` / `New` / 注入クロック
（`Options.Now`）/ `providers.Sanitize` / `httpclient` 経由の 1 往復、という構成は anthropic と同型。
provider 固有 JSON はパッケージ外に出さない。

| # | 入力（fixture / 状況） | 期待する振る舞い |
|---|---|---|
| T-G1-1 | `output_text` に `annotations[]`（`url_citation` 3 件、`url` / `title` / `start_index` / `end_index`） | success、`observability=observed`、`Result.Citations` に 3 件（url/title/index を保持）、`AnswerText` は本文 |
| T-G1-2 | 検索はした（`web_search_call` あり）が `annotations: []` | success、**observed かつ 0 件**（unknown にしない） |
| T-G1-3 | `message` はあるが `annotations` フィールド無し。トップレベル `citations[]` に 2 URL（うち 1 つは own_domain）、`web_search_call` あり | 引用 **0 件**（候補は引用にしない）、observed（検索アイテムがあるため）。候補一覧だけでは observed にならない |
| T-G1-4 | 401 + 入れ子封筒 `{"error":{"message","type","code"}}` | provider エラー、`error_code` は provider の code、`error_message` に message、鍵は redact、**リトライしない** |
| T-G1-5 | 403 + フラット封筒 `{"code":"...","error":"..."}` | provider エラー、`error_code` は code、message に error 文字列、鍵は redact |
| T-G1-6 | 1 回目 429 → 2 回目 200 | `httpclient` のリトライに乗って success（アダプター自身は再試行しない） |
| T-G1-7 | `output: []`（本文も引用も無い） | `error_code = empty_answer`、status=error |
| T-G1-8 | `status = incomplete` + `incomplete_details.reason = max_output_tokens` | error、`error_code = max_output_tokens`、message に理由 |
| T-G1-9 | `status = completed` だが `error` オブジェクトあり | error、provider の code/message を残す |
| T-G1-10 | `content[].type = refusal` | error、`error_code = refusal`（拒否は「引用 0 件の回答」ではない） |
| T-G1-11 | `status = in_progress` | error、`error_code = in_progress`（1 往復・非ストリームなので最終応答ではない） |
| T-G1-12 | usage に `input_tokens` / `output_tokens` / `total_tokens` | 3 つとも記録 |
| T-G1-13 | usage に `total_tokens` が無い（スキーマ上は required なので**防御的**な行） | `TotalTokens = nil`（0 で埋めない、足し算しない） |
| T-G1-14 | `server_side_tool_usage_details.web_search_calls = 2`、`num_server_side_tools_used = 5`、`num_sources_used = 7` | `ToolCalls = 2`。**どちらの合計値も使わない**（前者は全サーバーツール、後者は検索が見た source 数） |
| T-G1-15 | `server_side_tool_usage_details` 無し・`web_search_call` 2 件 | `ToolCalls = 2`（見えているものを数える） |
| T-G1-16 | `annotations` も `web_search_call` も無い（`usage` は `null`） | success、`observability=unknown`、`ToolCalls = 0`、`TotalTokens = nil` |
| T-G1-17 | 200 だが本文が JSON でない（`<html>`） | `error_code = response_not_json`、message に status と本文先頭 |
| T-G1-18 | `EndpointVersion()` | `responses/v1+web_search+xai` |
| T-G1-19 | 生 JSON に鍵文字列が含まれる | 保存される `RawJSON` から redact 済み |

**設計メモ**

- **判定順は固定する**: エラー封筒 → 成功本文の `error` → `refusal` → `status` → 空回答。この順にしないと、
  `refusal`（本文が空）が `empty_answer` に化け、`status=incomplete` が「短い回答」に化ける。
- `include` は**送らない**（既定でインライン引用が有効）。`no_inline_citations` を送ると注釈が
  「検索で見た URL の一覧」に変わり、引用の意味が壊れるため。なお `include` の対応値は Citations
  ページの記述と `openapi.json` の `ModelRequest.include` で一致しない（後者に引用系が無い）ので、
  送らない方を採る。
- `annotations` は**フィールドの存在**で observed を判定する（空配列 = 観測できた 0 件）。
- `message` アイテムが 1 つも無い応答は `unknown`（OpenAI アダプターと同じ規則）。`message` が無ければ
  本文も無いので、空回答の判定が先に効く。
- `error` オブジェクトはスキーマ未定義なので、4xx 封筒と同じ `{code,type,message}` / 文字列の
  両受けでデコードし、読めなければ `http_<status>` にフォールバックする。
- **引用 `title` は保存されない**。`Result.Citations` には載るが、`citations` テーブルに列は無く、
  生 JSON の中だけに残る。スキーマ変更はしない。
- 費用系（`usage.cost_in_nano_usd` / `cost_in_usd_ticks`）は**読まない**（D11）。

## T-G2. 設定 `[providers.grok]`

| # | 入力 | 期待する振る舞い |
|---|---|---|
| T-G2-1 | ブロック未設定 | `api_key_env = "XAI_API_KEY"`、`base_url = "https://api.x.ai/v1"`、`timeout_seconds = 90` |
| T-G2-2 | `model` 未設定 | `aicm run --provider grok` は設定エラー（exit 2、`model_missing`）。**モデル名を推測しない** |
| T-G2-3 | `Provider("grok")` | ブロックを返す（`ok = true`）。`Provider("perplexity")` は false のまま |
| T-G2-4 | `ProviderNames()` | 既存の末尾に `grok` を append（`openai, gemini, anthropic, grok`） |
| T-G2-5 | `Save()` | `[providers.grok]` ブロックを anthropic の後に書き、鍵の値は書かない |

## T-G3. CLI 配線

| # | 入力 | 期待する振る舞い |
|---|---|---|
| T-G3-1 | `aicm run --provider grok` | `buildProviders` が grok クライアントを組み立て、Grok だけが呼ばれる |
| T-G3-2 | 記録 | `runs.provider = 'grok'`、`runs.api_endpoint_version = 'responses/v1+web_search+xai'`、`cited_own` / `citation_count` が期待どおり |
| T-G3-3 | `aicm version` | JSON の `endpoints` に `grok`（アダプターの `grok.EndpointVersion` 定数を参照）、人間向け 1 行に `grok (responses/v1+web_search+xai)` |
| T-G3-4 | `aicm version` の live 表記 | Grok を「観測済み」と読ませない。`LiveObservation` に足さないので `live API:` 行の列挙に出ず、代わりに `not observed: grok (...)` の行に出る（テストで固定） |
| T-G3-5 | `aicm run`（プロバイダー未指定） | `[providers.grok] model` が設定されていれば既定の対象に含まれる |
| T-G3-6 | `XAI_API_KEY` 未設定 + `--dry-run` | `auth: grok XAI_API_KEY=missing` を出し、exit 0（既存挙動） |
| T-G3-7 | `XAI_API_KEY` 未設定 + 実実行 | exit 3（`auth_missing`）。**値は出さない** |

## T-G4. 不変条件テスト

| # | 入力 | 期待する振る舞い |
|---|---|---|
| T-G4-1 | `TestHTTPLayerIsUsedForTheProviders` | 対象パッケージに `grok` を追加（`net/http` を使うこと） |
| T-G4-2 | `TestNoProviderSDKIsImported` | 追加の依存を入れない（`go.mod` 不変）。xAI に公式 Go SDK は無い（2026-11-26 時点。公開されているのは Python / proto）ため `forbiddenImports` には足さず、その旨を決定記録に残す |

## T-G5. テスト（adapter + CLI）

- adapter: `httptest` + `testdata/` fixture（`cited.json` / `not-cited.json` / `candidates-only.json` /
  `error-auth.json` / `error-auth-flat.json` / `empty.json` / `no-citation-metadata.json`）。CONTRIBUTING の
  必須 5 形（cited / not cited / provider error / empty answer / no metadata）を必ず含む。
- CLI: 既存 `internal/cli` ハーネスに `[providers.grok]` と grok 用のレスポンスを足し、`--provider grok`
  を 1 本通す。**既存 3 プロバイダーの期待値は変えない**。
  - パスの取り合い: grok の base URL は `<server>/xai` にする。ただし `HasSuffix("/responses")` は
    `/xai/responses` にも一致するので、既存 3 プロバイダーの case を**完全一致**（`== "/responses"`）に
    変え、grok は `== "/xai/responses"` で受ける。順序に依存させない。
  - `t.Setenv("XAI_API_KEY", ...)` を足す（無いと exit 3 で落ちる）。
  - **読み戻し**: `newHarness` は最後に `init --force` を実行し、`config.Save` が設定を書き直す。
    `Save` に `[providers.grok]` が無いと `base_url` が既定の `https://api.x.ai/v1` に戻り、
    テストが実 API を叩こうとする。`init` 後の `aicm.toml` に grok ブロック・ハーネスの base_url・
    `XAI_API_KEY` が残り、鍵の値が書かれていないことをテストで固定する。
- すべてネットワーク・鍵なしで通ること。fixture は公式ドキュメントのレスポンス例を写したもの
  （候補一覧 `citations[]` の形は Responses のスキーマに無いため防御的な fixture とし、その旨を
  テストに書く）。

## T-G6. ドキュメント（英語 + `README.ja.md` 同時）

| ファイル | 追記内容 |
|---|---|
| `docs/providers.md` | Grok 節（endpoint / auth / search / 引用の正 / observability / usage / endpoint version / 注意点）。冒頭の Live API status に「Grok は実装済み・実 API 未検証」を明記。「Adding a provider」手順に `LiveObservation` の扱いを 1 行 |
| `docs/architecture.md` | パッケージ一覧に `internal/providers/grok` |
| `docs/cli-reference.md` | `run --provider` の列挙、`version` の対応プロバイダー列挙 |
| `examples/config.toml` | `[providers.grok]` ブロック（model は空、既定の env / base_url） |
| `.env.example` | `XAI_API_KEY=` |
| `README.md` / `README.ja.md` | 対応プロバイダー表に 1 行（Live 欄は「未検証（offline fixtures のみ）」）、スコープ文の列挙 |

## T-G7. 記録

- 本計画書（`documents/plan/20261126-provider-grok.md`）。
- 決定記録 D18（`documents/decisions/20261126-provider-grok.md`）: 引用の正・endpoint version・
  observability・status の扱い・usage・エラー封筒・確認日と URL・未検証事項。

---

## 異常系・エッジケース（横断）

| # | 状況 | 期待する振る舞い |
|---|---|---|
| E-1 | 応答が空（`output: []`） | `empty_answer` で失敗として記録（分母から外れる） |
| E-2 | 打ち切り（`incomplete`） | 理由を `error_code` にし、成功にしない |
| E-3 | 拒否（`refusal`） | `refusal` で失敗。引用率を下げない |
| E-4 | in_progress | `in_progress` で失敗（1 呼び出し 1 往復の原則） |
| E-5 | 401/403 | provider エラー。リトライしない・鍵を redact |
| E-6 | 429/5xx | `httpclient` に任せる（アダプターは再試行しない） |
| E-7 | エラー本文が既知の封筒でない（HTML 等） | `http_<status>` + 本文先頭 300 文字 |
| E-8 | 本文に URL が書かれているだけ | 引用にしない（不変条件 4） |
| E-9 | メタデータ欠如 | `unknown`（0 件ではない） |

## 触るファイル

**新規**: `internal/providers/grok/{grok.go,grok_test.go,testdata/*.json}`、
`documents/plan/20261126-provider-grok.md`、`documents/decisions/20261126-provider-grok.md`

**更新**: `internal/config/config.go`、`internal/cli/run.go`、`internal/cli/init.go`、
`internal/cli/forbidden_test.go`、`internal/cli/cli_test.go`、`examples/config.toml`、`.env.example`、
`docs/providers.md`、`docs/architecture.md`、`docs/cli-reference.md`、`README.md`、`README.ja.md`

**触らない**: `internal/providers/provider.go`（`LiveObservation` も含む）、`internal/domain`、
`internal/analysis`、`migrations/`、`internal/storage`、`NormalizerVersion`、既存アダプター、
Perplexity の成果物。

## 決定事項（D18 案）

| 論点 | 決定 |
|---|---|
| provider id | `grok` |
| `api_endpoint_version` | `responses/v1+web_search+xai`（OpenAI の `responses/v1+web_search` と混ぜない） |
| 引用の正 | `output[].content[].annotations[]` の `url_citation` のみ。トップレベル `citations[]` は候補であり引用ではない。本文中の `[[N]](url)` も解釈しない |
| observability | `annotations` フィールドの存在（空配列含む）または `web_search_call` アイテム → `observed`。どちらも無い → `unknown`。トップレベル `citations[]` だけでは observed にしない |
| status | `completed`（または空）= 成功。`incomplete` / `in_progress` / `error` オブジェクト / `refusal` はエラーコード化 |
| usage | 返る値のみ記録。`total_tokens` が無ければ nil。`tool_calls` は `web_search_calls` → 無ければ可視の `web_search_call` を数える |
| エラー封筒 | 入れ子（OpenAI 互換）とフラット（`code` + `error` 文字列）の両受け。どちらでも無ければ `http_<status>` |
| 費用 | 計算しない（D11 維持）。`cost_in_nano_usd` / `cost_in_usd_ticks` も読まない |
| `num_sources_used` / `num_server_side_tools_used` | 引用にも `tool_calls` にも使わない |
| 判定順 | エラー封筒 → 本文の `error` → `refusal` → `status` → 空回答 |
| 引用 `title` | `Result.Citations` には載るが DB には保存しない（スキーマ不変） |
| live 観測 | `LiveObservation` に `grok` を**足さない**（実 API 未観測） |

## 未検証事項・リスク

1. **実 API 未検証**。`XAI_API_KEY` を読まない・実呼び出しをしないため、fixture と `httptest` のみ。
   実際のレスポンスが公式例と異なる場合（例: `annotations` が付かない、`total_tokens` が欠ける）は
   未確認。
2. **エラー封筒の実形が不明**。2 形態の両受けで緩和するが、第 3 の形（例: `{"message": ...}` 単体）は
   `http_<status>` にフォールバックする。
3. **インライン引用の既定挙動に依存**。`include` を送らない前提。将来 xAI が既定を変えると
   `annotations` の意味が変わるため、そのときは `api_endpoint_version` を上げる。
4. `title` は引用番号（"1"）でありページ題名ではない（nullable なので空文字もありうる）。
   `Result.Citations` には渡すが、`citations` テーブルには列が無いので保存されない
   （生 JSON の中だけに残る）。ダウンストリームは URL のみを使う。
5. `model` の既定値は持たない。`docs.x.ai` の現行例は `grok-4.7` だが、**モデル名は設定で与える**。

## 進め方

1. 計画書 + D18 を書き、レビュー（plan-architect）を通す。
2. アダプター（T-G1）→ 設定（T-G2）→ CLI（T-G3）→ テスト（T-G4/T-G5）→ ドキュメント（T-G6）。
3. `gofmt -l .` / `go build ./...` / `go vet ./...` / `go test ./...` / `go test -race ./...` /
   `make check`（可能な範囲、未実行は未実行と報告）。
4. 検収（reviewer）→ 品質（QA）→ 親スレッドへ報告。
