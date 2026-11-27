# ブランド語の状態（`brand_terms_status`）の記録と表示の決定（D20）

| 項目 | 内容 |
|---|---|
| 日付 | 2026-11-28 |
| 入力 | issue #7「`brand_terms_status` を読む者が誰もいない」。オーナーは選択肢 1（レポートに出す）を選択 |
| 前提 | D5（スキーマの所有者）、D6（DB が正）、D7（レコードの中身）、D8（率の分母を 1 か所に固定）、D11（費用を計算しない）、D16（hash-only）、D19（公開スキーマが契約） |
| 決定 | `question_sets` に `brand_terms_status` を記録し、レポートの「条件」に provenance として出す。率の定義は変えない |

## 決定

1. **保存場所は `question_sets`**（migration 0003 で `brand_terms_status TEXT NOT NULL DEFAULT ''`
   を追加）。
   - `runs` に足さない理由: `runs` は 1 呼び出しの測定条件の行であり、`brand_terms_status` は
     質問セット全体の属性。run ごとに同じ値を繰り返すと、`aicm export` の列（D19 の公開契約）を
     広げることになり、既存の消費者の読み取りに影響する。
   - `projects` に足さない理由: 1 サイト 1 値しか持てない。質問セットを切り替えたときに古い status が
     残り、「どのセットの語が未確定か」を表せない。
   - `question_sets` は「取り込んだ質問セットの provenance」を持つ表（migration 0001 の
     コメント）であり、status はまさにその由来の情報。
2. **migration は前進のみ**。`ALTER TABLE ... ADD COLUMN` の既定 `''` は「未宣言」を意味する。
   0003 より前に書かれた行は列追加の既定値のまま未宣言として読め、データ移行は不要。
3. **同じ SHA の再 import は既存行を返す（`skipped`）挙動をそのまま保つ**。YAML の SHA256 は
   `question.LoadYAML` がファイル全体のバイト列から取るので、`brand_terms_status` を変えれば
   SHA が変わり新しい行になる。逆に同一 SHA ならバイト列が同じで status も同じなので、
   「行はあるが status が古い」を更新する経路は設計しない（append-only を崩さない。0003 より前の
   記録は項目 11）。
4. **レポートは記録から読む**。`analysis.LoadPeriod` が期間内の run の `question_set_sha256` を
   集め、`question_sets` から status を引いて `Conditions.BrandTermsStatuses` に入れる。
   report 時に現在の YAML を読み直さない（D6）。
5. **表示は条件表（記録から読んだ条件）の 1 行**。ラベルで「質問セットのブランド語の状態」と明示し、
   値を `proposed` / `confirmed` / `not_declared` に正規化する。
6. **混在時**は一意な status を固定順（`proposed` → `confirmed` → `not_declared`）で列挙する。
   1 つでも `proposed` があれば（GEO 節の言及率の直後に）注意書きを出す。「率は出したまま、
   根拠が未確定であることを隠さない」を守る。
7. **未指定・旧 DB・CSV 取り込みは `not_declared`（未宣言）**。`confirmed` とは表示しない。
   未知の値（列を手で書き換えた DB）も `not_declared` に倒す（未確定側に安全側で倒す）。
   正確には、未宣言になるのは「status が記録されていないセット」と「質問セットを指さない run」
   （YAML セットを一度も取り込まず CSV だけで run した場合）である。
8. **run 0 件の期間は status 行を出さない**。条件表は既存どおり「記録なし」。存在しない記録を
   捏造しない。
9. **設定の `brand_terms` と質問セットの `brand_terms` は別物として扱う**。照合に使う語は設定側
   （`config.brand_terms`）で、status はセットの宣言。両者が食い違っても「設定の語が proposed」とは
   読ませないよう、行のラベルに「（質問セット）」を付け、条件表（記録）側に置き、行が出るときだけ
   「照合に使う語は設定側のブランド語」という 1 文を添える。**語の一致比較は実装しない**（`question_sets` は
   語そのものを記録していないため、比較には語の保存という別のスキーマ拡張が要る）。
10. **率・分母・丸めは一切変えない**（D8）。今回の変更は provenance の記録と表示だけ。
    `aicm export` の列も変えない（D19）。
11. **記録済みの行は書き換えない（append-only を守る）**。SHA はファイル全体のバイト列なので、
    同じファイルの再 import は同じセットであり、status を上書きする経路は作らない。そのため、
    0003 より前に記録されたセットに status を後から付けることはできない。これを隠さないよう、
    `questions import` は**記録に書かれた値**を表示し、ファイルの宣言と食い違うときは警告を出す
    （新しい status を記録するには、セットの内容と `set_version` を変えて新しいセットにする）。
    レポートも記録だけを読む。

## 表示

| 状態 | EN | JA |
|---|---|---|
| 行ラベル | `brand terms status (question set)` | `ブランド語の状態（質問セット）` |
| `proposed` | `proposed (not confirmed)` | `未確定（proposed）` |
| `confirmed` | `confirmed` | `確定済み` |
| `not_declared` | `not declared` | `未宣言` |

`proposed` の注意書き（GEO 節、`brandNote` の直後）:

- EN: 「**The brand terms are not confirmed yet (proposed).** A mention rate depends on how the
  terms are chosen, so read the rate as a value taken before the list was confirmed.」
- JA: 「**ブランド語はまだ確定していない（proposed）。** 言及率は語の選び方に依存するため、
  確定前の値として読むこと。」

既存の `brandNote`（正規化・語境界の説明）とは文を混ぜない。前者は「数字の読み方の限界」、
後者は「語の確定度」で、役割が違う。

## 理由

- 言及率は語の選び方に強く依存する。率だけ出して語の根拠を隠すと、読者は数字を確定値として読む。
- status は「その run がどの質問セットで測られたか」の属性なので、質問セットの来歴として記録し、
  レポートの条件表（記録から読んだ条件）に出すのが既存の構造に合う。
- 既定を「未宣言」にしておけば、情報を持たない古い DB や CSV 経路でも `confirmed` と誤読させない。

## D5 / D6 / D7 / D16 / D19 との整合

| 決定 | 整合の内容 |
|---|---|
| D5（スキーマの所有者はこのツール） | migration 0003 をこのリポジトリに置く。前進のみ、`CREATE`/`ALTER` の既存流儀に従う |
| D6（DB が正） | レポートは DB の `question_sets` を読む。現在の YAML を読み直さない |
| D7（レコードの中身） | `question_sets` に 1 列追加。`runs` / `responses` / `citations` / `mentions` は不変 |
| D8（分母は 1 か所） | 分母と率の定義を変えない。表示と provenance だけ |
| D11（費用を計算しない） | 触らない |
| D16（hash-only） | 本文の保存方針と無関係。status は provenance なのでどちらのモードでも同じ |
| D19（公開スキーマが契約） | 追加は「既定値付きの 1 列」だけで、コンシューマー固有の列ではない。`aicm export` の列は不変 |

## 影響

- migration 0003（前進のみ）。`schema_migrations` に 3 が記録される。
- `aicm questions import` の出力（人間向け 1 行 / JSON）に `brand_terms_status` が加わる（追加のみ）。
  値は**記録に書かれた値**で、同じファイルの再 import で食い違うときは警告を出す。
- レポートの条件表に 1 行増える。既存のレポートの意味（確認済み / 未指定）は変えず、
  未指定が「未宣言」と明示されるようになる。
- スナップショット（golden）テストは無い。部分文字列で検査している既存テストの期待値は変えない。

## 未確定事項・リスク

1. 設定の `brand_terms` と質問セットの `brand_terms` の一致は検証しない。食い違いを検出するには
   語そのものの記録が必要で、本 issue の範囲外（別 issue 相当）。表示の文言で区別するに留める。
2. **0003 より前に記録されたセットは、status を後から記録できない**。append-only と「SHA = ファイル
   全体」の帰結で、同じファイルの再 import では列が空のままになる。警告で気づけるようにし、
   記録するにはセットを変える（新しい `set_version`）必要がある。ただし、それまでの run は
   引き続き「未宣言」と表示される（過去の記録を遡って書き換えないため）。
3. 期間内に複数 status が混在する場合、どの run がどの status かは出さない（期間の条件の要約として
   列挙する）。混在自体が「比較可能性の注意」なので、注意書きで足りると判断した。
4. **CSV で質問だけ入れ替えた場合の provenance は既存挙動のまま**。`question_sets` 行は作られず、
   run は直前の YAML セットの SHA を記録する。したがってレポートの status は「その YAML セットの
   宣言」であり、CSV の質問文の状態ではない。今回はこの意味を変えない（変えるなら別 issue）。
5. SHA は YAML 全体のバイト列由来なので、コメント変更でも新しい `question_sets` 行になる。
   これは既存挙動（質問セットの来歴はファイル内容のハッシュ）で、今回は変えない。
