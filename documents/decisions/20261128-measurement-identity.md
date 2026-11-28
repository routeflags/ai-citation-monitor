# 集計層の計測同一性と、混在の明示（D24）

| 項目 | 内容 |
|---|---|
| 日付 | 2026-11-28 |
| 入力 | issue #10（コードレビュー）P1-2「質問セットの異なるデータが混在する可能性」。`buildDomains` ならびに `groupKey()` は provider / model / stage だけで、`question_set_sha256` などの計測条件を集計キーに入れていない |
| 前提 | D8（率の分母）、D19（スキーマが公開契約）、D21（Lean は算術のみ。分母の選択・グループ横断はテストと文書で担保）、`CONTRIBUTING.md` の不変条件 2（グループをまたいで合算しない） |
| 決定 | `analysis.MeasurementIdentity` を導入し、グループにその集合と混在項目を持たせる。グループキーは変えない。混在は**隠さず出す**（率は拒否しない） |

## 決定

1. **`analysis.MeasurementIdentity`** を型として定義する。項目は issue が挙げた 4 つ:
   `api_endpoint_version`, `tool_version`, `question_set_version`, `question_set_sha256`。
   質問文の変更は `question_set_sha256`（ファイル全体の SHA）と run ごとの `question_version`
   が捉えるので、それ以上はキーを增やさない。
2. **グループキーは provider × model × stage のまま**（不変条件 2）。グループを条件で分割すると、
   レポートの見出し（`Group.Label()`）と表の形が変わり、利用者が見る「同じモデルの傾向」が
   分断される。新しいグループ化を導入するのは別の決定。
3. **`Group` に混在項目を追加**する。`MixedConditions []string`（JSON `mixed_conditions`）に、
   グループ内で値が揃わない項目を `tool_version: a, b` の形で入れる。項目の順は固定
   （endpoint → tool → question set version → question set sha）。空の値も 1 つの値として数え
   （質問セット無しの run と、記録済みの run は別の測定）、`—` と表示する。
4. **率は今までどおり計算する**。混在を理由に未測定へ倒さない。理由:
   - 率は「その期間に実際に観測した run」の性質であり、隠すと、利用者が自分のデータを
     見れなくなる。
   - `n = 0` は「未測定」であり、混在とは別の意味。混在を未測定で表すと既存の読者が壊れる。
   - 期間間の比較は `compare` がすでに差を出さない。集計層で同じ判断を再現すると二重基準になる。
   - 一方で「1 つの測定として提示する」のは不正。だから混在はレポートで明示する。
5. **レポート表示**。条件節に、グループ名付きで混在の警告行を出す（混在が無ければ何も出さない）。
   GEO の表はグループごとの表ではないので、表の直前ではなく条件節に置く。EN/JA の両方を用意し、
   `n` と率は出したままにする。また `conditionsNote` の「条件の違う記録は同じ表に入れない」は
   D24 と矛盾するので、「同じ測定として黙って平均しない」旨に直す。
6. **`compare` は同じ型と項目名を使う**が、比較するのは従来の 3 項目（`api_endpoint_version` /
   `question_set_sha256` / `tool_version`）だけで、空の値を無視する規則も変えない（`docs/metrics.md`
   の既存の約束を維持）。混在検出は 4 項目（セットの版ラベルも見る）。項目名の定義を共有するので、
   語彙は 2 か所でずれない。
7. **`trends` の分母の不具合も直す**。`NewRate(entry.cited, entry.valid-entry.unknown)` は
   status 非依存の `unknown` を成功数から引くため、成功 1 件（unknown）+ 失敗 1 件で分母が負になる。
   success ∧ observed を専用カウンタで数えて渡す。バケット間の条件比較は D27
   （`documents/decisions/20261128-trends-conditions.md`）で同じ 3 項目を使って行う。

## 理由

- レビューが指摘したのは「異なる条件のデータを受け取っても集計を拒否しない」ことで、
  危険の実体は**黙って平均すること**。同じ率でも「1 つの測定」と「2 つの測定の混合」は
  読者が区別できる必要がある。
- D21 が决めたとおり、これは「どの集合を数えるか」の性質で Lean の定理にはならない。
  Go のテスト（順序非依存、`n <= d`、失敗を分母に入れない、重複なし）と、レポートの表示で担保する。

## 影響

- `Group` の JSON に `mixed_conditions` が増える（追加のみ。既存フィールドは不変）。
- レポートは、条件が混在したグループにだけ警告行を出す（通常運用の出力は変わらない）。
  `conditionsNote` と `mixed_conditions` 以外の文言は変わらない。
- `compare` の挙動・文言は変わらない。
- `trends` は分母が正しくなる（成功 run が unknown で失敗と相殺されない）。
- スキーマ変更なし。`aicm export` の列は変わらない。
- テスト: `internal/analysis`（identity の収集・混在検出・順序非依存・不変条件・trends の分母）、
  `internal/reporting`（警告行の有無・EN/JA）、`internal/cli`（混在期間の end-to-end）。
