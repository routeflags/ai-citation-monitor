# Phase 1 (MVP) 決定記録 — ai-citation-monitor

| 項目 | 内容 |
|---|---|
| 日付 | 2026-11-15（改訂 2: D1 承認・ドキュメント言語の決定を反映） |
| 版 | 2.0 |
| 入力（正本） | `routeflags/ai-citation-monitor#1`（仕様 issue）、`documents/20261115.md`（Go 開発指示書。**改変しない**） |
| 参照（読み取り専用） | `routeflags/seo-operations` `290d6ea` の `scripts/geo/`（意味の参照のみ。語彙・Python 資産は持ち込まない） |
| 状態 | D1〜D11 すべて決定済み。追加決定は `20261115-phase1-decisions-2.md`（D12 Anthropic / D13 IDN・PSL / D14 失敗した run の再実行） |

---

## D1. プロバイダーへの経路 = ツール自身が公式 API を `net/http` で直接叩く【承認済み】

**オーナー判断（2026-11-15）**: `documents/20261115.md` が正しい。理由は「これは独立した公開
ツールであり、ほかツールへの依存ができない」。

当初案（netloom 経由のみ・カセット必須）は**破棄**した。破棄した内容:

- netloom の subprocess 呼び出し、カセット、`netloom env check` / `version --json` への依存
- `collect plan|preflight|run` というコマンド体系（`aicm run` に置き換え）
- 「HTTP クライアント / SDK の import 0 件」という禁止テスト（逆になった）
- カセット必須・同名カセット拒否の規則（証跡は DB に保存する生 JSON が担う）
- `verify-evidence`（ファイル突き合わせ）→ `aicm verify`（DB 内整合性）に置き換え

### 決定

| 論点 | 決定 |
|---|---|
| 経路 | `internal/providers/{provider.go,openai,gemini}` の HTTP アダプター。`http.Client` と `Transport` を再利用し、`context.Context` を全リクエストに伝播する。SDK は使わない |
| 禁止依存 | provider SDK（`openai-go` / `go-openai` / `genai` 等）と `os/exec`（他ツールへの委譲）は import 0 件をテストで機械検査。`net/http` は**必須**（アダプターが HTTP を話すことをテストで確認） |
| 仕様の根拠 | モデル名・エンドポイント・検索ツール指定・メタデータのフィールド名は**実装時に公式ドキュメントで確認**した。応答形状は seo-operations の実応答 fixture を一次資料として流用し、推測でフィールドを作らない |
| 記録 | `api_endpoint_version` を全 run に焼き込む（`responses/v1+web_search` / `generateContent/v1beta+google_search`）。仕様面が変われば版が変わり、古い測定と混ざらない |
| 実 API | **未検証**。live 実行は課金と認証を伴うため、オーナーの明示判断なしに行わない。検証は `httptest` と fixture のみ |

---

## D2. 言語・配布

- 言語: **Go**（`go.mod` に固定。実装時の安定版 `go 1.26`）。
- 配布: **単一バイナリ**、GoReleaser で GitHub Releases。macOS `arm64`/`amd64`、Linux `amd64`/`arm64`、Windows `amd64`。
- 依存（意図的に少ない）: `spf13/cobra`、`gopkg.in/yaml.v3`、`BurntSushi/toml`、`modernc.org/sqlite`（CGO 不要）、`golang.org/x/text`（NFKC）、`golang.org/x/time`（トークンバケット）。
- **指示書 §2 からの逸脱（意図的）**: Lip Gloss / 表表示ライブラリは採らない（Phase 1 はプレーンテキスト＋`--json`）。`errgroup` ではなく単純なワーカープール（`errgroup` は 1 件の失敗で全体を止めるが、失敗も記録して続行したいため）。`httptest` は**採用**（HTTP を持つため）。

---

## D3. CLI 名

**`aicm`**（`cmd/aicm`）。モジュールパス `github.com/routeflags/ai-citation-monitor`。
サブコマンドは `init` / `questions` / `run` / `report` / `compare` / `trends` / `competitors` / `export` / `verify` / `version`。

---

## D4. ライセンス

**Apache-2.0**（`LICENSE` に全文）。

---

## D5. DB スキーマの所有者 = このツール

- `migrations/*.sql` をツールが持ち、`schema_migrations(version, applied_at)` で適用済みを管理する。
- 利用側は `--db` で自リポジトリのファイルを指すだけ。
- マイグレーションは**前進のみ**。`0001` は `CREATE TABLE IF NOT EXISTS` で書いており、既存 DB をそのまま採用できる（テストで再適用の冪等性を固定）。

---

## D6. 記録の書き込み形式 = DB が正、JSONL/CSV/JSON は派生物

- `aicm run` は SQLite に**追記のみ**（`UPDATE` を発行しない。テストで固定）。
- `aicm export --format csv|json|jsonl` で外に出す。どれも DB から再生成できる。

---

## D7. レコードの中身 = 指示書 §5 のモデル

| テーブル | 採用 |
|---|---|
| `projects` / `questions` / `question_versions` / `competitors` | 採用 |
| `runs` | 採用。条件（provider / model / api_endpoint_version / search_enabled / measurement_method / question_set_version / question_set_sha256 / started_at / completed_at）を併記 |
| `responses` | 採用。`answer_text`・`answer_sha256`・`raw_response_json`（認証情報を除去）・`citation_observability`・`error_code`/`error_message`・usage 列（費用列は D11 で削除） |
| `citations` | 採用。`raw_url` / `normalized_url` / `hostname` / `source_type` / `position` / `is_own` / `malformed`（`response_id` で responses に紐づく） |
| `mentions` | 採用。`term` / `kind` / `occurrences` / `ambiguous` |
| `usage_records` | 採用。プロバイダーが申告したトークン・ツール呼び出しのみ（D11） |
| （追加）`question_sets` | 質問セットの由来（版・sha256・パス）を残すため追加 |
| （追加）`runs.plan_key` | 「同じ呼び出しを二度課金しない」ための一意キー（後述） |

---

## D8. 集計（指示書 §6 の 7 指標）

- 分母は **provider × model × stage** のグループで閉じる。またいだ合計を出さない。
- 率には必ず `n` を併記。`n=0` は **`—（未測定）` / `— (not measured)`**（`0.0%` と書かない）。
- 引用率の分母は「成功し、かつ引用メタデータが返った run」。**unknown は分母から外し、件数として出す**。
- ブランド言及率の分母は成功 run（本文がある）。
- カバー率の分母は「引用判定可能な回答が 1 件以上ある質問」。未計測と unknown のみは別件数。
- 競合引用獲得率は回答あたりの率なので合計は 100% を超え得る。
- 引用シェアの分母はグループの引用総件数（malformed も 1 件、ホスト無しも 1 行）。分母スコープを表示。
- トレンドは日/週/月。差は percentage points。初回は「履歴不足」。
- 条件の違う期間比較は差を出さず理由を出す。`--comparable-only` で除外できる。

---

## D9. ドキュメントとレポートの言語【オーナー判断で上書き】

**決定（2026-11-15 改訂）**: 公開ツールなので**英語を既定**にする。

| 対象 | 言語 |
|---|---|
| `README.md` | **英語**（本文）。`README.ja.md` を併置し、相互リンクを先頭に置く |
| `docs/*.md` | **英語**（指標定義は英語で正確に） |
| CLI のヘルプ・フラグ説明・エラーメッセージ | **英語**（次に何を直すかを含める） |
| `examples/` のコメント | 英語 |
| レポート本文 | 設定 `[report] language = "en"`（既定）/ `"ja"` で切替。固定文・列名・`n=0` の表記も切替（`— (not measured)` / `—（未測定）`） |
| コード内コメント | 英語 |
| `documents/`（決定記録・計画書） | 日本語のまま（内部文書） |

レポートの言語切替はデータではなく表示の問題なので、`Rate.Text` は言語中立の `—` を持ち、
表示層（`reporting` と CLI）が `i18n.Lang` で文言を決める。日本語レポートの既存テストで固定。

---

## D10. seo-operations 側の移行 = このリポジトリの範囲外

- 接点は「同じ質問セット YAML」「同じ考え方のレコード（条件の併記・追記のみ）」「SQLite」。
- seo-operations 側は薄いアダプタ（パス解決 + 呼び出し）だけを持ち、検証語彙は自前のまま。
- `make geo-*` からの呼び替えと `scripts/geo/` の削除はオーナー判断（issue の「seo-operations 側の移行」）。

---

## D11. 費用・予算機能は持たない【オーナー判断 2026-11-15】

オーナー判断: 「コスト機能が不要」。指示書 §3.9 の「予算上限」と §8 の予算制御は、
このツールの責務から外す。

破棄した内容:

- `internal/costs`（価格表・概算・予算判定）
- 設定の `[budget]`（`max_usd` / `currency`）と `[pricing."provider/model"]`
- DB の費用列（`responses.estimated_cost` / `cost_currency` / `cost_status`、
  `usage_records.cost_status` / `cost_currency` / `estimated_cost` / `price_basis`）
- 終了コード 5（予算停止）と `budget_exceeded` / `budget_stopped` の reason
- 実行前の費用表示、`export` の `estimated_cost` / `cost_status` 列

残したもの:

- `usage_records` のトークン数・ツール呼び出し（プロバイダーが申告した実測値。
  費用ではないので残す）
- 実行の大きさは計画（質問数 × repeat × プロバイダー）で決まり、`--limit` と
  `--dry-run` で送信前に確認できる
- リトライ回数は実行結果に件数として出る（二重課金の可能性はプロバイダー側の
  請求で確認する）

理由: 価格表はモデル改定のたびに古くなり、`unknown` だらけの概算を出すより、
ツールは「測ったこと」だけを持ち、金額はプロバイダーの請求画面に任せる方が
誤解が少ない。この判断で指示書の完了条件から予算管理が外れる（決定記録が優先）。

---

## 付記 A. 終了コード（指示書 §4 の 0–5 から、D11 で 5 を削除）

| コード | 意味 | 代表的な `reason` |
|---|---|---|
| 0 | 成功 | — |
| 1 | 検証失敗・一部失敗・証跡不整合 | `validation_failed` / `question_set_invalid` / `partial_failure` / `evidence_inconsistent` |
| 2 | 設定・使い方 | `config_incomplete` / `config_invalid` / `model_missing` / `questions_missing` / `usage` |
| 3 | 認証なし（1 回も送っていない） | `auth_missing` |
| 4 | API・通信の失敗 | `api_failure` / `run_failed` |

`--json` では `{"error":{"code":3,"reason":"auth_missing","message":"..."}}`。

---

## 付記 B. 指示書と issue の食い違い（改訂後）

| # | 指示書 | issue | 採用 |
|---|---|---|---|
| 1 | HTTP アダプターで公式 API を直接叩く | netloom 経由のみ | **指示書**（D1 承認） |
| 2 | `answer_text` / `raw_response_json` を保持 | 回答本文は保存しない | **指示書**（D7）。issue の不変条件のうち「条件の併記」「追記のみ」「分離」「n 併記」「標本の固定文」は維持 |
| 3 | `projects` / `questions` / `question_versions` / `competitors` / `responses` / `mentions` / `usage_records` | `question_sets` / `runs` / `citations` / `v_runs_valid` | **指示書**（D7）。`question_sets` と `v_runs_valid` は追加で残す |
| 4 | `run --provider` / `questions add\|list\|import` / `report` / `compare` / `trends` / `export` | `collect plan\|preflight\|run` 等 | **指示書**（D3）。`--dry-run` が plan+preflight の役割を担う |
| 5 | カセット必須・同名拒否・`verify-evidence` | 不変条件 2 / 不具合 3 | **置き換え**。証跡は `responses` 行と生 JSON、監査は `aicm verify`（DB 内整合性） |
| 6 | レポートは日本語固定 | `--period` 等の分離規則 | **両立**: 言語は設定（既定 en）、分離と n 併記はデータモデルで強制 |
| 7 | URL の query/fragment を無条件に削除しない | — | 生 `raw_url` は保存し、同一性判定だけ `scheme://host/path` で行う（`normalized_url`） |

---

## 付記 C. 未実装・未検証（Phase 1 の明示）

- **実 API は未検証**（live 実行なし）。アダプターは公式ドキュメントと実応答 fixture に基づく実装で、テストは `httptest` のみ。
- 公開サフィックス（PSL）による組織境界の判定は未実装（完全一致と `.<own_domain>` のラベル境界一致のみ）。
- IDN は `net/url` のホスト解析に委ねる（punycode 化はしない）。
- ブランド言及の曖昧一致検出は簡易（3 文字未満の語を `ambiguous` とするだけ）。同名衝突の本格判定は Phase 2。
- リダイレクト追跡・SSRF 対策は実装しない（このツールは引用 URL を取得しない）。
- 言語切替はレポートのみ。CLI メッセージの翻訳はしない。
- Web UI・Web サーバー・外部 DB・定期実行テンプレートは作らない（指示書 §3 の対象外）。
- Perplexity は対象外（Phase 1 のプロバイダーは OpenAI と Gemini）。
