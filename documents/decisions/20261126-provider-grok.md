# 追加決定記録（D18） — Grok (xAI) プロバイダー

| 項目 | 内容 |
|---|---|
| 日付 | 2026-11-26 |
| 前提 | `20261115-phase1-decisions.md` の D1–D11、`20261115-phase1-decisions-2.md` の D12–D16 を維持する。本記録は追加分のみ |
| 入力 | オーナー依頼「Grok（xAI） Responses API への対応」 |
| 計画書 | `documents/plan/20261126-provider-grok.md` |
| 状態 | **実装済み・実 API 未検証**（鍵を読まず、実呼び出しもしない。テストは fixture と `httptest` のみ） |

---

## D18. Grok (xAI) プロバイダーの追加【未検証で公開】

### 確認した公式仕様（2026-11-26）

| 項目 | 確認した内容 | 出典 |
|---|---|---|
| エンドポイント | `POST /v1/responses`（既定 base URL `https://api.x.ai/v1`） | `docs.x.ai/developers/rest-api-reference/inference/responses` |
| 認証 | `Authorization: Bearer $XAI_API_KEY`。401 は「ヘッダなし／無効なトークン」 | 同上 / `docs.x.ai/developers/debugging` |
| 検索ツール | `tools: [{"type": "web_search"}]`（SDK 名も `web_search`） | `docs.x.ai/developers/tools/web-search` |
| 引用（構造化） | `output[].content[].annotations[]`、`type = "url_citation"`、`url` / `start_index` / `end_index` / `title` | `docs.x.ai/developers/tools/citations`、`docs.x.ai/openapi.json` の `Annotation` |
| 引用（トップレベル） | `citations` は「検索中に見た全 URL」。**回答が使った根拠とは限らない**と公式が明記。ただし Responses の応答スキーマ（`ModelResponse`）に `citations` プロパティは無く、この一覧が Responses 本文に載るかは公式に明示が無い | `docs.x.ai/developers/tools/citations` / `openapi.json` |
| インラインリンク | 本文中の `[[N]](url)`。Responses API では既定で有効。`include: ["no_inline_citations"]` を送ると注釈は位置参照を持たない候補一覧になる（**ただし `openapi.json` の `ModelRequest.include` は引用系の値を列挙しておらず、Citations ページと一致しない**） | `docs.x.ai/developers/tools/citations` / `openapi.json` |
| 応答 | `output[]` に `web_search_call` / `message`（`content[].type = output_text` または `refusal`） | REST reference / `docs.x.ai/developers/tools/tool-usage-details` |
| status | `completed` / `in_progress` / `incomplete` | REST reference |
| `incomplete_details.reason` | `max_output_tokens` / `max_prompt_tokens` / `max_time_limit` / `content_filter` | `openapi.json` の `IncompleteDetails` |
| usage | `input_tokens` / `output_tokens` / `total_tokens` / `num_server_side_tools_used` / `server_side_tool_usage_details.web_search_calls` | REST reference |
| エラー封筒 | **公式にスキーマが無い**（4xx は説明文のみ、OpenAPI にも schema 無し） | REST reference / `openapi.json` |

取得は 2026-11-26。ページ末尾の "Last updated" は Web Search / Tool Usage Details が 2026-10-10。

### 決定

| 論点 | 決定 | 理由 |
|---|---|---|
| provider id | `grok` | ユーザーが選ぶ名前。`xai` は API 面の名前として endpoint version に焼き込む |
| `api_endpoint_version` | `responses/v1+web_search+xai` | OpenAI の `responses/v1+web_search` と同じ文字列にしない。`provider` 列でも区別できるが、export やドキュメントで紛らわしいため xAI を焼き込む |
| 引用の正 | `output[].content[].annotations[]` の `url_citation` **のみ**（`url` が空でないもの） | トップレベル `citations` は「検索中に見た URL」で、回答が使った根拠ではない（公式明記）。候補を引用に数えると、モデルが採用しなかったページで引用率が膨らむ。Anthropic の `web_search_tool_result` と同じ規則。Responses 本文にそもそも載らない可能性もあるため、**アダプターは `citations` をデコードすらしない**（載っても引用にならない） |
| 本文中のインラインリンク | パースしない | 不変条件 4「引用はプロバイダーのメタデータからのみ」。`[[N]](url)` は本文の文字列であってメタデータではない |
| observability | `annotations` フィールドが存在する（空配列含む）**または** `web_search_call` アイテムが存在する → `observed`。どちらも無い → `unknown` | 「メタデータが無い」と「引用ゼロ」を混ぜない既存の不変条件。トップレベル `citations[]` の有無だけでは observed にしない（それは回答の引用メタデータではない） |
| `message` アイテムが無い | `unknown` | OpenAI アダプターと同じ規則。本文が無い応答に引用メタデータは無い |
| `include` | **送らない**（既定のインライン引用を維持） | `no_inline_citations` を送ると注釈が「検索で見た URL の一覧」に変わり、引用の意味が壊れる。既定のまま使うことを endpoint version の前提として記録する。Citations ページと `openapi.json` で `include` の対応値が食い違うため、**どちらにも依存しない**（送らない）方を採る |
| status | `completed` / 空 → 成功。`incomplete` → `incomplete_details.reason`（無ければ `incomplete`）、`in_progress` → `in_progress`、`error` オブジェクト → provider の code、`refusal` content → `refusal` | 打ち切り・未完了・拒否は「引用 0 件の回答」ではない。成功にすると引用率が下がる。1 呼び出し 1 往復なので `in_progress` を継続しない |
| 判定順 | エラー封筒 → 成功本文の `error` → `refusal` → `status` → 空回答 | `refusal` は本文を持たないので、空回答の判定を先にすると `empty_answer` に化ける。`status=incomplete` も同様に「短い回答」に化ける |
| 空回答 | 本文も引用も空 → `error_code = empty_answer` | 既存規則（取得失敗であって「引用なし」ではない） |
| usage | 返る値のみ記録。`input_tokens` / `output_tokens` / `total_tokens`。**`total_tokens` が無ければ nil**（スキーマ上 required なので防御的措置） | 合計は API が返す値。返らないときに足し算で埋めるのは測定値の捏造（D12 と同じ） |
| `tool_calls` | `usage.server_side_tool_usage_details.web_search_calls` → 無ければ可視の `web_search_call` アイテムを数える。**`num_server_side_tools_used` も `num_sources_used` も使わない** | 「返る集計値があればそれを、無ければ見えているものを数える」（Anthropic / OpenAI と同じ方針）。前者は全サーバーツールの合計、後者は検索が見た source 数で、どちらも web search の回数でも引用数でもない |
| エラー封筒 | 入れ子（`{"error":{"code","type","message"}}`、OpenAI 互換）とフラット（`{"code":..., "error":"message"}`）の**両受け**。どちらでもない → `http_<status>` + 本文先頭 | 公式にスキーマが無いため、片方に賭けない。成功本文の `error` オブジェクトもスキーマ未定義なので同じデコーダで読む。401/403 はリトライしない（`httpclient` の既存方針） |
| 費用 | 計算しない（D11 維持）。`usage.cost_in_nano_usd` / `cost_in_usd_ticks` は**読まない**（列も無い） | xAI はコストを返すが、費用機能を持たないのが決定（D11）。provider が返す値でも「測定値」として保存する対象ではない |
| live 観測 | `providers.LiveObservation` に `grok` を**足さない** | 実 API を観測していない。「観測済み」と誤読させない |

### 却下した案

- **トップレベル `citations[]` を引用として記録する**: 公式が「回答が使った根拠とは限らない」と明記して
  おり、引用率を系統的に過大にする。候補と引用は別物（Anthropic の `web_search_tool_result` と同じ）。
- **`include: ["no_inline_citations"]` を送って本文の `[[N]](url)` をパースする**: 本文パースは不変条件 4
  に反する。また無効化すると注釈が位置参照を失い、メタデータの意味が変わる。
- **`endpoint version` を OpenAI と同じ `responses/v1+web_search` にする**: Responses 系は同じでも
  提供元が違い、引用の意味も usage の形も違う。混在した測定を後から分離できない。
- **`status=in_progress` を成功扱いにする / `incomplete` を短い回答として扱う**: 打ち切られた回答は
  引用集合が不完全で測定に使えない。
- **`model` の既定値（例 `grok-4.7`）を持つ**: モデル名は推測しない既存方針（設定エラー exit 2）。
  ドキュメントの現行例が変わると記録の意味が黙って変わる。

### 未検証・制限

- **実 API は未検証**。`XAI_API_KEY` を読まず、実呼び出しもしない。fixture は公式ドキュメントの
  レスポンス例を写したもの。
- **エラー封筒の実形は不明**。2 形態の両受けで緩和しているが、第 3 の形は `http_<status>` に落ちる
  （メッセージに本文先頭は残る）。
- `annotations` が既定で「回答が使った根拠」を指すという前提は、Citations ページの記述
  （インライン引用が有効なら位置参照付き）に依拠している。xAI が既定を変えた場合は
  `api_endpoint_version` を上げる。
- `title` は引用番号（"1"）でページ題名ではない（nullable なので空文字もありうる）。`Result.Citations`
  には渡すが、`citations` テーブルに title 列は無いので**保存されない**（生 JSON の中だけに残る）。
  ダウンストリーム（`cited_own` / 競合判定）は URL のみを使うため影響しない。
- **xAI に公式 Go SDK は無い**（2026-11-26 時点で公開されているのは Python SDK と proto）。
  したがって `forbiddenImports` に xAI の項目は足していない（足しても空振りする）。SDK を
  持ち込まない方針自体は `TestNoProviderSDKIsImported` が引き続き守る。
- **回答本文はインライン引用の記法（`[[N]](url)`）ごとそのまま保存する**。書き換えると
  `answer_sha256` が変わり、`aicm verify` が「編集された回答」として報告する。
- **スキーマ変更は不要**（`runs.provider` に CHECK 制約は無く、新しい値はそのまま入る）。
  migration を足さないこと。
- マルチターン（`previous_response_id`）と `max_turns` は扱わない。1 呼び出し 1 往復の原則。
