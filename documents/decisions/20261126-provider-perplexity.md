# Perplexity Agent API 追加決定記録（D17） — ai-citation-monitor

| 項目 | 内容 |
|---|---|
| 日付 | 2026-11-26 |
| 前提 | `20261115-phase1-decisions.md`（D1–D11）と `20261115-phase1-decisions-2.md`（D12–D16）を維持する。本記録は追加分のみ |
| 入力 | オーナー依頼「Perplexity — Agent API」（Grok/xAI は別スレッド） |
| 計画書 | `documents/plan/20261126-provider-perplexity.md` |
| 状態 | **実装済み・2026-11-28 に 1 回観測**（下記付記）。テストは fixture と `httptest` のみ |
| 範囲 | Perplexity のみ（Grok/xAI は別スレッド）。**D18 は本記録では使わない** |

---

## D17. Perplexity Agent API プロバイダーの追加【2026-11-28 に 1 回観測】

### 確認した公式仕様（確認日 2026-11-26）

| 項目 | 確認した内容 | 出典 |
|---|---|---|
| エンドポイント | `POST https://api.perplexity.ai/v1/agent`（`/v1/responses` は OpenAI 互換エイリアス） | <https://docs.perplexity.ai/api-reference/agent-post> |
| 認証 | `Authorization: Bearer $PERPLEXITY_API_KEY` | 同上 |
| リクエスト | `{"model", "input", "tools": [{"type": "web_search"}]}`。`stream` 既定 false | <https://docs.perplexity.ai/docs/agent-api/tools/web-search> |
| 回答本文 | `output[]` → `type="message"` → `content[]` の `type="output_text"` の `text` | 同上（Response Shape） |
| 引用（任意） | `content[].annotations[]`（`type="url_citation"`, `url` / `title` / `start_index` / `end_index`）。モデルがマーカーを付けたときのみ | OpenAPI `ContentPart.annotations` / `Annotation` |
| 検索結果（引用の正） | `output[]` の `type="search_results"` → `results[]{id,url,title,snippet,date,last_updated,source}`。マーカーの有無にかかわらず `id`/`url` を引用の正とすると明記 | 同上（Response Shape） |
| `status` | `completed` / `failed` / `incomplete` / `in_progress` / `queued` / `cancelled` | OpenAPI `Status` |
| usage | `input_tokens` / `output_tokens` / `total_tokens`（required）、`tool_calls_details.<tool>.invocation`、`cost` | OpenAPI `ResponsesUsage`。検索は `search_web` キーで報告されると明記 |
| エラー封筒 | `{"error": {code, message, type}}` | OpenAPI（400 の応答として記載） |
| モデル | マルチプロバイダー（`openai/gpt-5.6-sol` など） | quickstart / web_search ページ |

### 決定

| 論点 | 決定 | 理由 |
|---|---|---|
| provider id | `perplexity` | API 面を指す ID。モデル名が `openai/...` でも `provider` 列は変えない |
| 既定 base_url / パス | `https://api.perplexity.ai/v1` + `/agent` | 既存規約（OpenAI は `.../v1` + `/responses`）に合わせ、パスを記録とコードの両方で明示する |
| `api_endpoint_version` | `agent/v1+web_search` | API 面・版・サーバーツールを焼き込む。別版の測定と混ざらない |
| 引用の正 | `search_results` アイテムの `results[]`（`url` / `title`）と、`output[].content[].annotations[]` の `url_citation`。同じ URL は 1 件に数える | 公式 web_search ページは「マーカーの有無にかかわらず `search_results` の `id`/`url` を引用の正とせよ」と明記している。Perplexity には Anthropic のような「本文ブロックごとの citation 列」が無いため、検索結果が引用リストそのもの。`annotations` は任意（モデルがマーカーを付けたとき）なので、片方だけを読むと実運用で引用を取りこぼす |
| トップレベル `results[]` | 読まない | OpenAPI の応答スキーマに無い（quickstart の例には現れる）。`search_results` の `results[]` が正式な位置 |
| observability | `annotations` フィールドの存在（空配列含む）または `search_results` 出力アイテム → `observed`。どちらも無い → `unknown` | 検索が走ったことは引用メタデータの存在を意味する。`unknown` を 0 件扱いしない不変条件を守る |
| 空回答 | 本文も引用も空 → `error_code=empty_answer` | 既存規則（D12 と同じ） |
| `status` の扱い | `completed` 以外は成功扱いにしない。`failed` → `error.code`（無ければ `failed`）、`incomplete` → `incomplete`、`in_progress` → `in_progress`、`queued` → `queued`、`cancelled` → `cancelled_by_provider` | 打ち切り・未完了を 0 件の測定として混ぜない。`cancelled` はツール側の中断（`ErrorKindCancelled`）と区別する |
| usage | `input/output/total_tokens` を返った値だけ記録。`tool_calls` は `tool_calls_details.search_web.invocation`（無ければ `web_search` キー、それも無ければ `search_results` アイテム数） | 集計値があればそれを、無ければ見えているものを数える（OpenAI アダプターと同じ）。`total_tokens` が無ければ nil（捏造しない） |
| 費用 | 計算しない。`cost` / `cost_usd` は読まない、記録もしない | D11 維持 |
| マルチプロバイダー性 | `docs/providers.md` に「`provider` 列は API 面を指し、モデル名が他社を指しても別プロバイダー扱いにしない」と 1 段落明記 | 記録の意味を読み違えると、モデル別の比較を provider 別と誤解する |

### 検証（fixture と、2026-11-28 の 1 回の live 観測）

- `internal/providers/perplexity` の単体テスト: cited（annotations と search_results の併合・重複排除）/ search-results-only / annotations-only / not-cited（空 results）/ no metadata / empty / 401 / 403（鍵 redact）/ 429→200 / 非 JSON / 500+HTML / `status` 5 種 / usage の欠落。
- `internal/cli` のハーネス: `--provider perplexity` で `/agent` のみが呼ばれ、`runs.provider`・`api_endpoint_version`・`cited_own`・`citation_count` が期待どおり。`openai,perplexity` の同時実行も確認。
- fixture は公式ドキュメントの応答例を写したもの（実 API のダンプはコミットしていない）。

### 未検証・制限

- 実 API は 2026-11-28 に 1 回観測済み（`openai/gpt-5.6-sol`、1 質問）。`providers.LiveObservation` に追加した。
- `search_results` が実運用で常に返るかは、1 回の観測では分からない（2026-11-28 の 1 回は 15 件返った）。返らない場合は `unknown`（0 件ではない）。`annotations` は任意で、付かなくても `search_results` から引用を取る。
- 401/429 のエラー本文形は未明記。封筒が読めなければ `http_<status>` に落とす。
- トップレベル `results[]` は OpenAPI の応答スキーマに無い（クイックスタートの例には現れる）。現れても読まない。
- `queued` / `in_progress` は本ツールがポーリングしないため常に失敗記録になる（非同期実行はしない）。

---

## 付記: 2026-11-28 の実 API 観測

| 項目 | 観測結果 |
|---|---|
| モデル | `openai/gpt-5.6-sol` |
| 質問 | 1 問（smoke、`What is Example Shop?`） |
| HTTP / status | 200 / `completed` |
| 引用メタデータ | `search_results` 15 件。`message` の `annotations` は空配列（0 件） |
| usage | input 4,321 / output 151 / total 4,472、`tool_calls_details.search_web.invocation = 1` |
| 記録 | `citation_observability=observed`、`citation_count=15`、`tool_calls=1` |

`annotations` だけを読む実装なら 0 件になっていた。`search_results` を引用の正とする
現在の規則で 15 件を記録できた。費用は記録しない（D11）。

`providers.LiveObservation` は「このビルドが実際に 1 回観測した事実」の記録なので、
観測後に `perplexity` を追加した（`openai/gpt-5.6-sol, 2026-11-28`）。1 モデル・1 質問の
観測であり、対応モデル一覧ではない。
