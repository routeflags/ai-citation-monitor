# `brand_terms_status` をレポートに出す 計画書（T-B1–T-B7）

| 項目 | 内容 |
|---|---|
| 日付 | 2026-11-28 |
| 対象 | `ai-citation-monitor`（Go 製 CLI `aicm`）、`main` = `7d94599`（v0.2.0 公開後・クリーン） |
| 入力 | issue #7「`brand_terms_status` を読む者が誰もいない」。オーナーは選択肢 **1（レポートに出す）** を選択 |
| 制約 | push / merge / GitHub issue 操作をしない／実 API を叩かない／鍵を読まない／バージョン bump とリリースをしない／率の分母と定義を変えない（D8）／export の列を変えない（D19）／費用機能を足さない（D11） |
| 記録先 | 決定記録 **D20**（`documents/decisions/20261128-brand-terms-status.md`） |

BDD（振る舞い単位）で分解する。各タスクは「入力 → 期待する振る舞い」を持ち、正常系・異常系・
エッジケースを並べる。参照した既存実装は `internal/question/question.go`（YAML 契約と検証）、
`internal/cli/questions.go`（import 経路）、`internal/storage/sqlite/{store,verify}.go`
（`question_sets` の書き込み・読み出し）、`internal/analysis/period.go`（`Conditions` は
記録から読む）、`internal/reporting/{markdown,labels}.go`。

---

## 1. 問題

`aicm questions import` は YAML の `brand_terms_status`（`proposed` | `confirmed`）を
`question.Validate` で値だけ検証し、そのまま捨てている。DB にもレポートにも渡らず、どこからも
読まれない。ブランド語がまだ推測（`proposed`）でも、レポートは確定済みと同じ書き方をする。

言及率は語の選び方に強く依存する。率だけ出して語の根拠を隠さない、というのが issue #7 の要求で、
オーナーは「レポートに出す」（選択肢 1）を選んだ。

## 2. 決定の要約（詳細は D20）

| 論点 | 決定 |
|---|---|
| 保存場所 | `question_sets.brand_terms_status`（migration 0003）。`runs` と `projects` は採らない |
| 再 import | YAML 全体の sha256 が変わるので新しい行になる。同一 SHA の再 import は `skipped` で、記録済みの行は書き換えない（append-only）。import は記録値を表示し、ファイルの宣言と食い違えば警告を出す |
| レポートへの渡し方 | `analysis.LoadPeriod` が期間内の run の `question_set_sha256` から `question_sets` を引く。現在の YAML は読み直さない（D6） |
| 混在 | 一意な status を `proposed` → `confirmed` → `not_declared` の固定順で列挙。1 つでも proposed なら注意書きを出す |
| 未指定・旧 DB・CSV | `not_declared`（未宣言）。`confirmed` とは表示しない |
| run 0 件 | status 行を出さない（既存どおり「記録なし」） |
| 設定語とセット語 | 行のラベルを「質問セットのブランド語の状態」とし、条件表（記録から読んだ条件）に置く。注記で「照合に使う語は設定側のブランド語」と明示。語の一致比較は実装しない（過剰） |

---

## 3. タスク（BDD）

### T-B1. 記録: `question_sets` に status 列を足す（migration 0003）

`ALTER TABLE question_sets ADD COLUMN brand_terms_status TEXT NOT NULL DEFAULT ''`。
前進のみ、既存流儀（`0002` と同じ形）に従う。既定の空文字は「未宣言」を意味し、列追加前の行
（旧 DB）はそのまま未宣言として読める。

| # | 入力 | 期待する振る舞い |
|---|---|---|
| T-B1-1 | 旧 DB（0002 まで適用済み、`question_sets` 行あり）で `sqlite.Open` | 0003 が適用され、既存行の `brand_terms_status` は `''`。他の列・行は変わらない |
| T-B1-2 | `Migrate()` を 2 回呼ぶ | 何も壊れない（既存テスト `TestMigrateIsIdempotentAndKeepsRecords` が維持） |
| T-B1-3 | `aicm export --format csv` | 列が変わらない（export は runs / citations 由来。D19 の契約に触れない） |

### T-B2. 書き込み: import が status を記録する

`RecordQuestionSet(ctx, projectID, setVersion, sha, path, brandTermsStatus)` に引数を足し、
`internal/cli/questions.go` が `set.BrandTermsStatus` を渡す。

| # | 入力 | 期待する振る舞い |
|---|---|---|
| T-B2-1 | `brand_terms_status: proposed` の YAML を import | `question_sets.brand_terms_status = 'proposed'`、stdout / JSON に status が出る |
| T-B2-2 | `brand_terms_status: confirmed` | `'confirmed'` |
| T-B2-3 | `brand_terms_status` 未指定の YAML | `''`（未宣言）。confirmed で埋めない |
| T-B2-4 | 同じファイルを 2 回 import | `skipped` のままで行は増えない（既存挙動） |
| T-B2-5 | `set_version` も status も同じで本文の質問文だけ変えた YAML | SHA が変わるので新しい行。古い行の status は残る（append-only） |
| T-B2-6 | `brand_terms_status: draft` | 既存どおり `E043` で import 失敗、何も書かない |
| T-B2-7 | `--check` / `--dry-run` | 何も書かない（`questions` も `question_sets` も 0 件のまま） |
| T-B2-8 | CSV import | `question_sets` 行は作らない（既存どおり）。run は直前の YAML セットの SHA を記録する既存挙動なので、YAML セットを一度も取り込んでいなければ `not_declared` |
| T-B2-9 | 0003 より前に記録されたセット（列が空）に、同じファイルを再 import | `skipped` のままで列は空。stdout は**記録値**（`not declared`）を出し、stderr に「記録は `not declared`、ファイルは `confirmed`。記録済みのセットは更新しない」旨の警告を出す |
| T-B2-10 | 同じファイルを再 import（status も同じ） | `skipped`、記録値と宣言値が一致するので警告なし |

### T-B3. 読み出し: `analysis.Conditions` に status を載せる

`LoadPeriod` は run の一意な `question_set_sha256` を集め、`question_sets` から status を引いて
`Conditions.BrandTermsStatuses` に入れる。値は `proposed` / `confirmed` / `not_declared` に正規化し、
固定順（proposed → confirmed → not_declared）で並べる。

| # | 入力 | 期待する振る舞い |
|---|---|---|
| T-B3-1 | 期間内の run が 1 種類の SHA、その行が `proposed` | `["proposed"]` |
| T-B3-2 | 期間内の run の SHA が 2 種類（`proposed` と `confirmed`） | `["proposed", "confirmed"]` |
| T-B3-3 | run はあるが SHA が空（CSV で取り込んだ質問で run した） | `["not_declared"]` |
| T-B3-4 | SHA はあるが `question_sets` に行が無い（手で runs を入れた DB） | `["not_declared"]` |
| T-B3-5 | 期間内に run が 0 件 | 空（`nil`）。status を捏造しない |
| T-B3-6 | 旧 DB の `question_sets` 行（status が `''`） | `["not_declared"]` |

### T-B4. 表示: 条件表の行と注記（EN / JA）

条件表（`**Conditions read from the records.**`）の末尾に 1 行足す。ラベルは設定表の
「ブランド語」と区別するため「質問セット」を明示する。値は 3 種。

| # | status | EN | JA |
|---|---|---|---|
| T-B4-1 | `proposed` | `proposed (not confirmed)` | `未確定（proposed）` |
| T-B4-2 | `confirmed` | `confirmed` | `確定済み` |
| T-B4-3 | `not_declared` | `not declared` | `未宣言` |
| T-B4-4 | 混在 | `proposed (not confirmed), confirmed` | `未確定（proposed）, 確定済み` |

- 行のラベル: EN `brand terms status (question set)` / JA `ブランド語の状態（質問セット）`。
- 行が出るときだけ、表の直後に 1 文の注記（記録の出所と、照合に使う語は設定側であること）を出す。
- 行は run が 1 件以上あるときだけ出す（0 件の期間は既存どおり「記録なし」）。

### T-B5. 表示: proposed の注意書き（GEO 節）

言及率のすぐ近く（`brandNote` の直後）に、proposed のときだけ注意書きを出す。既存の `brandNote`
（正規化・語境界の説明）とは役割が違うので文を混ぜない。

| # | 入力 | 期待する振る舞い |
|---|---|---|
| T-B5-1 | status に `proposed` を含む期間 | EN「ブランド語はまだ確定していない（proposed）。言及率は語の選び方に依存するため、確定前の値として読むこと」に相当する段落が出る |
| T-B5-2 | `confirmed` のみ | 注意書きを出さない（率の意味は変わらない） |
| T-B5-3 | `not_declared` のみ | 注意書きを出さない（行の「未宣言」で足りる） |
| T-B5-4 | run 0 件 | 注意書きを出さない（率そのものが出ない） |

### T-B6. テストで固定する振る舞い

| # | 層 | テスト名 | 内容 |
|---|---|---|---|
| T-B6-1 | `internal/storage/sqlite` | `TestRecordQuestionSetKeepsTheBrandTermsStatus` / `TestOldQuestionSetsReadAsNotDeclared` / `TestUpgradingAnOldDatabaseLeavesQuestionSetsNotDeclared` | `RecordQuestionSet` が status を書き、同一 SHA の再 import で行が増えない。`BrandTermsStatuses` が SHA → status を返し、未知の SHA は map に無い。0002 までの DB を開くと 0003 が適用され、既存行は未宣言のまま残る |
| T-B6-2 | `internal/analysis` | `TestBrandTermsStatusesReadsOnlyWhatTheRecordsSay` / `TestQuestionSetHashesSkipsEmptyAndDuplicates` | `brandTermsStatuses`（純関数）の表駆動テスト: 単一 / 混在 / SHA 空 / 行なし / run 0 件 / 旧 DB / 未知の値 |
| T-B6-3 | `internal/reporting` | `TestBuildShowsTheRecordedBrandTermsStatus` / `TestBuildOmitsTheBrandTermsStatusWhenThePeriodHasNoRuns` | EN proposed（行 + 注記 + 注意書き）/ JA proposed / confirmed / 未宣言 / 混在 / run 0 件で行が出ない |
| T-B6-4 | `internal/cli` | `TestReportShowsTheBrandTermsStatusRecordedAtImport` / `TestReportListsBothStatusesWhenThePeriodMixesQuestionSets` | import（proposed）→ run → report で `proposed (not confirmed)` と注意書きが出る（EN / JA）。混在（proposed の後に confirmed のセットを import して別セッションで run）で両方が読める |
| T-B6-5 | `internal/cli` | `TestQuestionsImportCheckWritesNeitherQuestionsNorTheQuestionSet` | `--check` / `--dry-run` が `question_sets` にも書かない |
| T-B6-6 | `internal/cli` | `TestReimportingARecordedSetReportsTheStoredStatus` | 列を空にした既存行（v0.2.0 相当）に同じファイルを再 import すると、記録値（`not declared`）を表示して警告を出し、レポートも `not declared` のまま |

既存テストの期待値は変えない（golden ファイルは無く、部分文字列での検査のみ）。やむを得ず
変わる場合はテストコメントに理由を書く。

### T-B7. 記録とドキュメント

| ファイル | 内容 |
|---|---|
| `documents/plan/20261128-brand-terms-status.md` | 本計画書 |
| `documents/decisions/20261128-brand-terms-status.md` | D20（保存場所・混在時の表示・設定語とセット語・未指定/旧 DB・D5/D6/D7/D16/D19 との整合） |
| `examples/questions.yml` | `brand_terms_status` のコメントを実態（記録され、レポートの条件に出る）に直す |
| `docs/metrics.md` | status は provenance であり分母を変えないことを 1 節で明記 |
| `docs/architecture.md` | `question_sets` の行に `brand_terms_status` を足す |

---

## 4. 異常系・エッジケース（横断）

| # | 状況 | 期待する振る舞い |
|---|---|---|
| E-1 | 旧 DB（0003 前の `question_sets` 行） | `not_declared`。`confirmed` と表示しない |
| E-2 | CSV で質問だけ取り込んで run | `not_declared`（質問セットの provenance が無い） |
| E-3 | 期間内に複数 status | 固定順で列挙、1 つでも proposed なら注意書き |
| E-4 | run 0 件の期間 | status 行も注意書きも出さない |
| E-5 | status が未知の値（列を手で書き換えた DB） | 正規化で `not_declared` に倒す（confirmed と誤読させない） |
| E-6 | `question_sets` を読めない（DB エラー） | `LoadPeriod` がエラーを返す（既存の DB エラーと同じ扱い） |
| E-7 | 設定の `brand_terms` とセットの語が違う | 表示は「質問セットの状態」であり語の一致は主張しない（注記で明示） |
| E-8 | 率・分母 | 一切変えない（D8）。n も率の丸めも同じ |

## 5. 不変条件

1. 率の分母・定義を変えない（D8）。今回の変更は表示と provenance だけ。
2. 記録は append-only。migration は前進のみ。
3. レポートは「記録から読んだ条件」を守る。現在の YAML を読み直さない。
4. 公開スキーマが契約（D19）。export の列を変えず、コンシューマー固有の列を足さない。
5. 費用機能を足さない（D11）。実 API を呼ばない。鍵を読まない。
6. `documents/` は日本語、`docs/` / README / コードコメントは英語。
7. v0.2.0 の挙動（Perplexity / Grok を含む）を変えない。

## 6. 触るファイル

**新規**: `migrations/0003_question_set_brand_terms_status.sql`、`internal/analysis/period_test.go`、
`documents/plan/20261128-brand-terms-status.md`、`documents/decisions/20261128-brand-terms-status.md`

**更新**: `internal/question/question.go`（status 定数）、`internal/cli/questions.go`（記録）、
`internal/storage/sqlite/{store.go,store_test.go}`、`internal/analysis/period.go`、
`internal/reporting/{markdown.go,labels.go,markdown_test.go}`、`internal/cli/cli_test.go`、
`examples/questions.yml`、`docs/metrics.md`、`docs/architecture.md`

**触らない**: `internal/export`、`internal/analysis/{metrics,compare,trends}.go`、
`internal/providers/`、`internal/runner/`、`internal/reporting/filename.go`、
`migrations/0001`・`0002`、バージョン情報（`CHANGELOG` のリリース節）。

## 7. 検証

`gofmt -l .` / `go build ./...` / `go vet ./...` / `go test ./...` / `go test -race ./...` /
`make check`。この Delta worktree では `GOFLAGS=-buildvcs=false` が必要な場合がある。

## 8. リスク・未確定

1. **設定語とセット語の一致は検証しない**。`question_sets` は語そのものを記録しないため、
   設定とセットの語が食い違っても検出できない。表示の文言で「状態はセットの宣言、照合は設定の語」
   と分けるに留める（語を記録するのは別 issue 相当の拡張）。
2. **0003 より前に記録されたセットは status を後から記録できない**。append-only と「SHA = ファイル
   全体」の帰結。再 import では列が空のままなので、import が記録値を表示して警告を出し、
   レポートは記録だけを読む（過去の run は「未宣言」のまま）。記録するにはセットを変えて
   （新しい `set_version`）新しい行を作る。
3. **CSV で質問だけ入れ替えた場合の provenance は既存挙動のまま**。run は直前の YAML セットの SHA を
   記録するので、レポートの status はその YAML セットの宣言を表す（CSV の質問文の状態ではない）。
4. **SHA は YAML 全体のバイト列**。status を変えると SHA が変わるので履歴が増える。コメントだけの
   変更でも SHA が変わるのは既存挙動で、今回も変えない。
5. **期間内に複数 status がある場合の意味**。率はグループ単位で出るが、status は期間の条件として
   列挙する。どの run がどの status かの対応は出さない（条件表は期間の要約という既存の意味に合わせる）。
6. レポートの既存文言は変えない。条件表に 1 行増えるため、既存レポートの読み手には差分が見える
   （意味は「未宣言」の明示であり、率は変わらない）。
