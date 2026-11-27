# 記録契約の決定（D19） — 公開ライブラリとしてのスキーマ

| 項目 | 内容 |
|---|---|
| 日付 | 2026-11-28 |
| 入力 | issue #6「記録の契約を旧 scripts/geo のレコードと揃えるか決める」 |
| 前提 | D5（スキーマの所有者）、D6（DB が正）、D7（レコードの中身）、D10（seo-operations 移行は範囲外）、D16（hash-only） |
| 決定 | 旧 scripts/geo / netloom の契約には合わせない。このリポジトリの公開スキーマが契約 |

## 決定

1. **公開ライブラリの契約はこのリポジトリのスキーマ**。`migrations/` と D7 の列が正で、
   `docs/architecture.md` / `docs/metrics.md` が意味を定義する。利用側のローカルツール
   （netloom を含む）の都合で列を足さない。
2. **`netloom_version` / `netloom_commit` は持たない**。このツールは netloom を経由せず
   プロバイダー API を直接呼ぶ。証跡の同一性は `tool_version`（正規化仕様込み）と
   `api_endpoint_version`、質問セットの版/SHA、`plan_key` で表す。netloom の列を足すと、
   公開スキーマが特定のローカル実行環境に依存し、他の利用者の記録と比較できなくなる。
3. **旧 `scripts/geo` の JSONL は互換対象にしない**。必要なら利用側（または別の
   エクスポートアダプタ）で `aicm export` の出力を変換する。ストレージのスキーマを
   旧形式に寄せない。変換仕様が必要になった時点で、別 issue としてマッピングを定義する。
4. **本文保存の既定は full のまま**（D16）。O20 は seo-operations 側のポリシーであり、
   公開ライブラリの既定を決めるものではない。Git 追跡 DB に置く利用者は
   `--store hash-only` / `store_answer_body = false` を選ぶ。

## 理由

- 公開ライブラリでは、記録の意味が README / docs / migrations で完結している必要がある。
  特定のローカルツールの列を混ぜると、外部利用者が同じ DB を読めない。
- netloom はこのリポジトリの依存でも入力でもない。コードと docs に netloom の契約は無い
  （`forbidden_test.go` の文字列は「外部計測ツールに委譲しない」ための検査であって契約ではない）。
- 旧 `scripts/geo` との比較は移行時の一度きりの作業であり、計測器の公開契約ではない（D10）。

## 影響

- スキーマ変更なし。migration 不要。
- issue #6 の 3 問への回答: (1) 既定は full のまま、(2) このスキーマが正で変換は範囲外、
  (3) `netloom_*` は不要。
- 将来、外部利用者から旧 JSONL の変換要望が出た場合は、`aicm export` の別フォーマットとして
  追加する（ストレージは変えない）。
