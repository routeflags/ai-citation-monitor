# Phase 1 追加決定記録（D12–D14） — ai-citation-monitor

| 項目 | 内容 |
|---|---|
| 日付 | 2026-11-15 |
| 前提 | `20261115-phase1-decisions.md` の D1–D11 を維持する。本記録は追加分のみ |
| 入力 | オーナー依頼「仕様差分の解消と Anthropic (Claude) プロバイダー追加」（T1–T4）、および live 検証で見つかった不具合 2 件 |
| 計画書 | `documents/plan/20261115-phase1-deltas.md` |

---

## D12. Anthropic (Claude) プロバイダーの追加【未検証で公開】

### 確認した公式仕様（2026-11-15、`platform.claude.com`）

| 項目 | 確認した内容 |
|---|---|
| エンドポイント | `POST /v1/messages` |
| 認証 | `x-api-key`。`anthropic-version` ヘッダは必須で、現行は `2026-10-21`（`2023-06-01` も利用可） |
| 必須パラメータ | `max_tokens` |
| 検索ツール | `tools: [{"type": "web_search_20250305", "name": "web_search", "max_uses": N}]`（サーバーツール。`web_search_20260209` / `web_search_20260318` も存在） |
| 応答 | `content[]` に `text` / `server_tool_use` / `web_search_tool_result` が並ぶ |
| 引用 | `text` ブロックの `citations[]`（`type = "web_search_result_location"`、`url` / `title` / `cited_text` / `encrypted_index`） |
| 検索エラー | **HTTP 200 の本文内**で `web_search_tool_result.content` が `{"type": "web_search_tool_result_error", "error_code": "max_uses_exceeded"}` という単一オブジェクトになる |
| usage | `input_tokens` / `output_tokens` / `server_tool_use.web_search_requests`。**合計は返らない** |
| stop_reason | `end_turn` / `max_tokens` / `stop_sequence` / `tool_use` / `pause_turn` / `refusal` |

### 決定

| 論点 | 決定 | 理由 |
|---|---|---|
| ツール型 | `web_search_20250305`（basic web search） | 新しい 2 版は動的フィルタ / 応答制御の追加で、測定の意味が変わる。basic が最も単純で、型に日付が入っているため版が記録に残る |
| `api_endpoint_version` | `messages/v1+web_search_20250305+api_2026-10-21` | API 面と `anthropic-version` の両方を焼き込む。どちらが変わっても古い測定と混ざらない |
| `max_tokens` | 既定 **8192**、`[providers.anthropic] max_tokens` で変更可 | 必須パラメータ。検索付き回答が途中で切れない大きさ。切れた回答は測定ではないので後述のとおりエラーにする |
| `max_uses` | 既定 **5**、`[providers.anthropic] max_uses` で変更可 | 公式の目安は単一事実で 1–3、複数比較で 10 以上。5 は追撃 1 回分の余裕を残し、課金と遅延を有界にする |
| 引用の出所 | `text` ブロックの `citations[]` **のみ** | `web_search_tool_result` は「検索が返した候補」であり「回答が使った根拠」ではない。候補を引用に数えると、モデルが採用しなかったページで引用率が膨らむ。OpenAI アダプターの「本文中の URL は引用ではない」と同じ方針 |
| 引用メタデータ無し | `web_search_tool_result` ブロックがある、または `text` ブロックに `citations` キーがある → `observed`（空配列なら 0 件）。どちらも無い → `unknown` | 「メタデータが無い」と「引用ゼロ」を混ぜない既存の不変条件を守る |
| 検索エラー（200 本文内） | 回答としては **success**、その検索は引用 0 件、`observability=observed` | 検索が走ったことはメタデータであり、回答本文は実在する。失敗として捨てると分母が理由なく減る |
| usage | `input_tokens` / `output_tokens` を記録。`total_tokens` は **nil**（未申告） | API が合計を返さない。足し算で埋めるのは測定値の捏造 |
| `tool_calls` | `usage.server_tool_use.web_search_requests`。無ければ `server_tool_use` ブロックを数える | OpenAI の `web_search_call` 集計と同じ「見えているものは数える」方針 |
| 費用 | 計算しない（D11 維持） | 変更なし |

### stop_reason の扱い

| stop_reason | 扱い | 理由 |
|---|---|---|
| `end_turn` | 成功 | 正常終了 |
| `max_tokens` | **エラー** `error_code = max_tokens` | 打ち切られた回答は引用集合が不完全で、測定として使えない。`max_tokens` を上げるのが正しい対処 |
| `refusal` | **エラー** `error_code = refusal` | 拒否は「引用 0 件の回答」ではない。成功にすると引用率が下がる |
| `pause_turn` | **エラー** `error_code = pause_turn` | 長時間検索でターンが中断した状態。継続には 2 回目のリクエストが必要で、このツールは 1 呼び出し 1 往復なので「未測定」が正しい |
| `tool_use` | **エラー** `error_code = unexpected_tool_use` | サーバーツールのみ送っているのでクライアントツール要求は想定外 |
| その他 / 空 | 成功（本文と引用が空なら `empty_answer`） | 既存規則を維持 |

### 未検証・制限

- **実 API は未検証**。`ANTHROPIC_API_KEY` が無いため、テストは fixture と `httptest` のみ。
  「実装済み・実 API 未検証」と README・`docs/providers.md` に明記した。
- `pause_turn` の継続（2 回目のリクエストで再開する）は**実装しない**。1 呼び出し 1 往復の
  原則を守るため。必要なら次の判断事項。
- マルチターンは扱わない（`encrypted_content` / `encrypted_index` は生 JSON に保存するだけ）。
- 引用の位置（`start_index` / `end_index`）は Anthropic が返さないため nil。

---

## D13. IDN と公開サフィックスの扱い

### 現状（Phase 1）の問題

- 公開サフィックスは未実装（完全一致と `.<own_domain>` のラベル境界一致のみ）。
- IDN は `net/url` のホスト解析任せ（punycode 化しない）。

よって `https://例え.テスト/` と `https://xn--r8jz45g.xn--zckzah/` は別ドメイン扱いになり、
`own_domain = "github.io"` は全顧客を自社扱いにする。

### 決定: 推奨案（PSL + punycode）を採用

| 論点 | 決定 |
|---|---|
| 依存 | `golang.org/x/net`（`publicsuffix` と `idna`、CGO 不要）を 1 つ追加 |
| ホスト比較 | `domain.CanonicalHost`（小文字化・末尾ドット除去・`idna.Lookup` で punycode）を通してから比較。引用の `hostname` / `normalized_url` も同じ形で保存 |
| `own_domain` が公開サフィックス | **設定エラー（exit 2）**。`icann` と private セクションの両方を見る（`github.io` / `blogspot.com` / `s3.amazonaws.com` を含む） |
| 単一ラベル | `com` のようなレジストリ TLD のみエラー。`localhost` のようなローカル名は通す（共有関係が無い） |
| ラベル境界 | 不変（`notexample.com` は不一致） |
| `NormalizerVersion` | **1 → 2**。own-domain 一致の意味が変わるため、記録の比較可能性を版で切る |
| 壊れた punycode | 小文字化のみでフォールバックし、引用は捨てない（分母を静かに変えない） |

**別案（PSL を使わず「未対応」と明記するだけ）を採らなかった理由**: 依頼の要求（異なる組織の
誤統合を避ける）を満たさず、`own_domain = "github.io"` の誤統合を塞ぐには結局 PSL 相当の
判定が要る。依存 1 つ（CGO 不要・標準的な x/net）で済むため、実装する方が安い。

### 制限（既知）

- `competitors` が公開サフィックスのときは**検査しない**（`own_domain` のみ exit 2）。
  競合に `co.jp` を入れると同様に過剰一致する。必要なら次の判断事項。
- PSL は `x/net` 同梱のスナップショット。改訂はライブラリ更新で追随する。
- `own_domain` を Unicode で書くことは可能（どちらの表記でも一致する）が、DB の `projects.own_domain`
  は設定値をそのまま保存する（表示用）。一致判定は常に正規化後。

---

## D14. 失敗した run の再実行をどう扱うか

### 見つけたこと

`runs` には `plan_key UNIQUE` とは別に
`UNIQUE (question_id, question_version, provider, model, repeat_index, stage, session)`
がある（「1 条件 1 行」の自然キー）。したがって「失敗はまだ測っていないので再試行する」を
素直に実装すると、2 行目がこの制約で弾かれ、`success=2` と表示しつつ DB には何も残らない
（**静かな証跡欠落**）という最悪の形になる。実際に試して確認した。

### 決定: スキーマは変えず、CLI が事実を言う

- 失敗も 1 条件 1 行として保持する（追記のみ・条件の一意性という既存の不変条件を優先）。
- `aicm run` は計画に「失敗として記録済み」の件数を出し、`--session` を案内する:
  `note: 2 of the recorded calls failed and are not resent in this session; pass --session <label> to measure them again`
- `--dry-run --json` は `failed_recorded` を返す。

**採らなかった案**:

1. **スキーマ変更**（自然キー UNIQUE を外す / 試行番号を条件に入れる）— SQLite ではテーブル
   再構築のマイグレーションが必要で、「1 条件 1 行」という意味も失われる。公開済み DB との
   互換性リスクに対して得るものが小さい。
2. **plan_key に試行番号を付ける** — `plan_key` を変えても自然キーで弾かれるため無効（実測）。
3. **失敗行を `status` 更新で書き換える** — 追記のみの不変条件に反する。

### 残る落とし穴と次の判断

- 認証を直して同じ日に再実行しても、`--session` を変えない限り再送されない（exit 0）。
  これは「この実行は何も送っていない」という意味では正しいが、`exit 0` だけを見ると成功に
  見える。**改善案**: 失敗のみで構成された計画のときに exit 4（`api_failure`）を返す。
  ただし「直したのに失敗扱い」という逆の誤解を生むため、現時点では注記に留めた。
- 判断してほしいこと: 上記の exit code 変更を行うか、それとも現行（注記のみ）でよいか。

---

## D15. Gemini の grounding chunk は「リダイレクト + 引用元の名前」

**オーナー判断（2026-11-15、live 観測で発見）**: 実 Gemini API の応答では
`candidates[].groundingMetadata.groundingChunks[].web.uri` が
`https://vertexaisearch.cloud.google.com/grounding-api-redirect/...` という**不透明な
リダイレクト**で、実際の引用元は同じ chunk の `web.title`（例 `go.dev`）に入っている。
URI のホストをそのまま採用すると、Gemini の全引用が Google のリダイレクトホストになり、
`cited_own` が決して 1 にならない（＝ Gemini の GEO 引用率・競合シェアが常に 0）。

### 決定

- アダプターは chunk の `web.domain`（あれば）→ `web.title`（ホスト名の形のときだけ）を
  `Citation.SourceHost` として運ぶ。`web.title` が文（`Go 1.27 release notes`）なら hint に
  しない（誤ってドメインとして扱わない）。
- `domain.NormalizeCitationInputs` は hint があれば **hostname を hint** にし、
  `normalized_url` と `dedupe_key` は**生の URL のまま**（リダイレクトは正規化できない）。
  `malformed` にはしない。
- 記録には provider が返した URL と、provider が意図した引用元の両方が残る。
- `NormalizerVersion` を **2 → 3** に上げる（`cited_own` の意味が変わるため）。
- 対象は Gemini のみ。OpenAI / Anthropic の citation URL は実ページを指すので hint は空。

### 検証

- live（`gemini-3.1-pro-preview`、2026-11-15）: 3 引用のうち `go.dev` が `is_own=1`、
  `cited_own=1`、`brand_mentioned=1`。修正前は同じ条件で `cited_own=0`（全て
  `vertexaisearch.cloud.google.com`）。
- 回帰テスト: `TestAskKeepsTheRealSiteBehindAGroundingRedirect`（adapter）と
  `TestCitationSourceHostHint`（domain）。

### 制限

- `web.title` がホスト名でない（例 `Go Blog`）場合、その chunk はリダイレクトホストのまま
  記録される。実測では主要な chunk はホスト名だったが、保証ではない。
- `competitors` の判定も同じ hint を使うため、同じ制限を受ける。
