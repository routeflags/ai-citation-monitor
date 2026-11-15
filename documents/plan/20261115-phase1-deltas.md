# Phase 1 差分の解消（T1–T4）計画書

| 項目 | 内容 |
|---|---|
| 日付 | 2026-11-15 |
| 対象 | `main` = `e6955bb`（Phase 1 公開済み・CI 緑） |
| 入力 | オーナー依頼「仕様差分の解消と Anthropic (Claude) プロバイダー追加」（T1–T4） |
| 制約 | 実 API を叩かない／鍵を読まない／費用・予算・終了コード 5 を復活させない／OpenAI・Gemini の既存挙動を変えない |
| 記録先 | 決定記録 D12（Anthropic）、D13（IDN / 公開サフィックス） |

BDD（振る舞い単位）で分解する。各タスクは「入力 → 期待する振る舞い」と異常系を持ち、実装層ごとに
シナリオを整理する。

---

## T1. `aicm questions import --strict`

**現状**: `question.Set.Validate(strict bool)` は実装済み（`E040` / `W040`）だが、CLI は常に
`Validate(false)` を呼ぶため `--strict` 相当の経路がない。

| # | 入力 | 期待する振る舞い |
|---|---|---|
| T1-1 | YAML・stage 入れ子が崩れている・フラグなし | `warning: W040 ...` を stderr、exit 0、書き込み成功 |
| T1-2 | 同上 + `--strict` | `NG: E040 ...` を stderr、exit **1**、**1 行も書かない**（DB 不変） |
| T1-3 | 同上 + `--strict --check` | exit 1（検証は `--check` より前に走る） |
| T1-4 | 同上 + `--strict --dry-run` | exit 1 |
| T1-5 | 同上 + `--strict` + `--json` | stdout は `{"error":{"code":1,"reason":"question_set_invalid",...}}` のみ |
| T1-6 | 入れ子が正しい YAML + `--strict` | exit 0（誤検知しない） |
| T1-7 | CSV + `--strict` | exit 0。CSV に stage 定義は無いので昇格対象が無い（受理して無変化） |

**触るファイル**: `internal/cli/questions.go`（`strict` フィールドとフラグ、`Validate(opts.strict)`）、
`internal/cli/cli_test.go`（または新規テスト）、`docs/cli-reference.md`。
**警告のまま起きること（docs に 1 文）**: stage の集合が崩れると stage ごとの質問集合が変わり、
カバー率の分母（引用判定可能な回答がある質問）が前回と同じ意味でなくなる。

---

## T2. `aicm trends` の n 不一致注記

**現状**: `analysis.Compare` は `reason=n_differs` + `detail="2 vs 1"` + 注記を出すが、
`analysis.Trends` はバケット間の n 差に何も言わない。

| # | 入力 | 期待する振る舞い |
|---|---|---|
| T2-1 | 連続バケットで n が同じ | 注記なし・delta 表示（既存どおり） |
| T2-2 | 連続バケットで n が違う（2 → 1） | `reason=n_differs`、`detail="2 vs 1"`、`note="n differs (2 vs 1)"`、**delta は出す** |
| T2-3 | 直前バケットが未測定（n=0） | `reason=earlier_unmeasured`、delta は出さない（既存挙動を壊さない） |
| T2-4 | 初回バケット | `reason=no_history`、n 注記は出さない |
| T2-5 | 日本語表示 | `n が違う（2 vs 1）`（`compare` と同一語彙） |

語彙は `compare` の `n_differs` をそのまま使い、新しい reason を発明しない。
**触るファイル**: `internal/analysis/trends.go`、`internal/analysis/metrics_test.go`、
`docs/metrics.md`、`docs/cli-reference.md`。

---

## T3. IDN / 公開サフィックスの扱い

**現状**（決定記録 付記 C・`internal/domain/domain.go`）: 公開サフィックスは未実装、IDN は
`net/url` のホスト解析任せ（punycode 化しない）。よって `https://例え.テスト/` と
`https://xn--r8jz45g.xn--zckzah/` は別ドメイン扱い、`own_domain = "github.io"` は全サブドメインを
自社扱いにする。

**採用**: 推奨案（PSL + punycode）。`golang.org/x/net/publicsuffix` と `golang.org/x/net/idna`
（CGO 不要）。

| # | 入力 | 期待する振る舞い |
|---|---|---|
| T3-1 | 引用 `https://例え.テスト/a`、own_domain `xn--r8jz45g.xn--zckzah` | `is_own = true`（punycode に寄せて同一母集団） |
| T3-2 | 逆（引用が punycode、own_domain が Unicode） | 同上 |
| T3-3 | 大文字混じり・末尾ドットの IDN | 正規化して一致（既存規則の維持） |
| T3-4 | `own_domain = "github.io"` / `"co.jp"` | **設定エラー（exit 2）**: 公開サフィックス自体を自社にできない |
| T3-5 | `own_domain = "localhost"`（ドット無し） | エラーにしない（単一ラベルは PSL 比較の対象外） |
| T3-6 | `notexample.com` vs `example.com` | 不一致（ラベル境界判定は不変） |
| T3-7 | 壊れた punycode ラベル | 小文字化のみでフォールバックし、引用は捨てない |

`NormalizerVersion` を `1` → `2` に上げる（own-domain 一致の意味が変わるため、記録の比較可能性を
版で切る）。`aicm version` のハードコード `"1"` を `domain.NormalizerVersion` 参照に直す。
**触るファイル**: `internal/domain/{domain.go,domain_test.go}`、`internal/config/config.go`、
`internal/cli/init.go`、`docs/architecture.md`、`documents/decisions/`、`go.mod`。

---

## T4. Anthropic (Claude) プロバイダー

**確認した公式仕様**（2026-11-15 時点、`platform.claude.com` の Web search tool / Create a Message /
API versions）:

- `POST {base}/v1/messages`、ヘッダ `x-api-key`、`anthropic-version`（現行 `2026-10-21`）、
  `content-type: application/json`
- 本文は `max_tokens` 必須、`tools: [{"type": "web_search_20250305", "name": "web_search", "max_uses": N}]`
- 応答 `content[]` は `text` / `server_tool_use` / `web_search_tool_result` のブロック列
- 引用は `text` ブロックの `citations[]`（`type = "web_search_result_location"`、`url` / `title` /
  `cited_text` / `encrypted_index`）
- **検索エラーは 200 で返る**: `web_search_tool_result.content` が
  `{"type": "web_search_tool_result_error", "error_code": "..."}` という単一オブジェクト
- usage は `input_tokens` / `output_tokens` / `server_tool_use.web_search_requests`
- `stop_reason`: `end_turn` / `max_tokens` / `stop_sequence` / `tool_use` / `pause_turn` / `refusal`

| # | 入力（fixture） | 期待する振る舞い |
|---|---|---|
| T4-1 | 検索 + `citations[]` あり | success、`observability=observed`、引用を `citations[]` からのみ取る |
| T4-2 | 検索したが `citations: []` | success、**observed かつ 0 件**（unknown にしない） |
| T4-3 | 401/403 | provider エラーとして記録、鍵は redact、リトライしない |
| T4-4 | 429 | `httpclient` のリトライに乗る（最終的に provider エラー記録） |
| T4-5 | 本文も引用も空 | `error_code = empty_answer`（既存規則） |
| T4-6 | `citations` フィールドも検索ブロックも無い | success、`observability=unknown`（引用ゼロと混同しない） |
| T4-7 | `stop_reason = max_tokens` / `refusal` / `pause_turn` | それぞれ `max_tokens` / `refusal` / `pause_turn` のエラーコードで失敗扱い（下記 D12） |
| T4-8 | `--provider anthropic` / `openai,anthropic` | 既存の複数プロバイダー同時実行に乗る |

**決めたこと**: `max_tokens` 既定 **8192**（設定可）、`max_uses` 既定 **5**（設定可）、
`api_endpoint_version = messages/v1+web_search_20250305+api_2026-10-21`、
`total_tokens` は **nil**（API が返さないので合計を捏造しない）。
**触るファイル**: `internal/providers/anthropic/`（新規 + `testdata/`）、`internal/config/config.go`、
`internal/cli/{run.go,init.go}`、`internal/cli/forbidden_test.go`、`examples/config.toml`、
`docs/{providers,cli-reference,architecture}.md`、`README.md`、`README.ja.md`、`documents/decisions/`。

---

## 追加タスク（live 検証で発見された不具合・作業中に追加）

別のサブが実 API（無効キー）で認証失敗経路を検証して見つけた 2 件と、設計点 1 件を追加で扱う。

| # | 入力 | 期待する振る舞い |
|---|---|---|
| T5-1 | 実 API の 401 本文（`{"error":{...},"status":401}`） | `error_code = invalid_api_key`、`error_message` に provider の message。`response_not_json` に落ちない |
| T5-2 | エラー応答が既知の封筒でない（例 `502` + HTML） | `error_code = http_502`、`error_message` に status と本文先頭 |
| T5-3 | 全呼び出しが provider エラー（401） | exit **4**（`api_failure`）。一部成功の混在は exit 1（`partial_failure`）のまま |
| T5-4 | 全呼び出しが transport 失敗 | exit 4（`api_failure`、既存どおり） |
| T6-1 | 失敗した run が記録済みの条件で同じ session を再実行 | 再送しない（DB は 1 条件 1 行）が、計画に「失敗として記録済み」と `--session` の案内を出す |
| T6-2 | 同じ条件を `--session <新ラベル>` で再実行 | 再送し、新しい行として追記（上書きしない） |

**T6 の設計判断（D14）**: 実装してみたところ `runs` には
`UNIQUE (question_id, question_version, provider, model, repeat_index, stage, session)`
があり、「失敗は未測定なので再試行」を素直に実装すると 2 行目が弾かれ、`success=2` と
表示しつつ DB に何も残らない（静かな証拠欠落）になると実測で確認した。スキーマ変更
（テーブル再構築）は公開済み DB との互換性に見合わないため、**スキーマは変えず CLI が事実を言う**
方を採った。理由と採らなかった案は決定記録 D14。

**触ったファイル**: `internal/providers/{openai,gemini}/*`、`internal/runner/*`、
`internal/storage/sqlite/store.go`、`internal/cli/{run.go,cli_test.go}`。

---

## 進め方

1. T1・T2（小さい・既存語彙の再利用）→ T3（依存追加 + 正規化の意味変更）→ T4（新規アダプター）
2. 各段階で `gofmt -l .` / `go build ./...` / `go vet ./...` / `go test ./...`
3. レビュー観点: 不変条件（追記のみ／条件併記／グループをまたぐ合計なし／率に n／n=0 は未測定／
   失敗も記録／秘密を書かない）が壊れていないこと。OpenAI・Gemini のテストが無変更で緑であること。
