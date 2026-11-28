# trends もバケット間で計測同一性を比べ、違えば差を出さない（D27）

| 項目 | 内容 |
|---|---|
| 日付 | 2026-11-28 |
| 入力 | issue #12「trends: バケット間で計測条件を比較して差を出す」。D24 で `MeasurementIdentity` と `compare` の拒否規則を入れたが、`trends` は材料を持ちながら隣り合うバケットを比べていない |
| 前提 | D24（計測同一性と混在の明示）、D22（差は表示した tenths の差）、D8（分母）、`CONTRIBUTING.md` の不変条件 1・2 |
| 決定 | 各バケットは identity の集合を持ち、直前のバケットと **compare と同じ 3 項目**を比べる。違う場合は `conditions_differ` で差を出さない。`n_differs` の挙動は変えない。JSON は additive |

## 決定

1. **バケットの identity を出す**。`TrendPoint` に `identities`（重複なし、
   `api_endpoint_version` → `tool_version` → `question_set_version` → `question_set_sha256`
   の順）と `mixed_conditions`（D24 と同じ項目名・語彙）を additive に追加する。
   重複は除き、並びは identity の項目順で固定する（DB の読み出し順に依存しない）。
   全項目が空の identity は「条件が記録されていない」を意味する（D24 の空値の扱いと同じ）。
   バケットは週・日単位なので、期間の途中の `tool_version` 変更は 1 つのバケットに
   2 つの identity として現れることがある。率はそのバケットの観測なので出したまま、
   混在を注記にも書く（D24 §4 と同じ判断）。
2. **直前のバケットと比較する項目は compare と同じ 3 つ**: `api_endpoint_version` /
   `question_set_sha256` / `tool_version`。空の値は「無い」であり、両側で空なら
   相違ではない（`compare` の `conditionMismatch` をそのまま使う）。
   `question_set_version` のラベルだけの違いは比較しない（質問を同定するのは SHA）。
3. **条件が違うバケットは差を出さない**。`reason = conditions_differ`、`detail` に
   `tool_version: a -> b` の形で差分、cited / brand の差は `null`、`*_delta_text` は
   未測定マーク。バケット自身の率と `n` は出す。`detail` の片側に値が無いときは
   `—` と表示する（`mixed_conditions` の空値と同じ表記）。
4. **優先順位は `no_history` → `earlier_unmeasured` → `conditions_differ` → `n_differs`**。
   差が無い（未測定）ときは条件の話をしない。`n_differs` は条件が一致したときだけ
   今までどおり出す（`detail` は 2 つの分母）。
5. **文言は compare と共有する**。`conditionsDifferNote` / `nDiffersNote` を
   `internal/analysis/compare.go` に置き、`CompareRow.NoteFor` と `TrendPoint.NoteFor`
   の両方が呼ぶ。EN/JA の写し違いを作らない。
6. **レポートの Trends 注記も更新する**。`labels.go` の `trendsNote`（EN/JA）に、
   条件が違うバケットは差を出さないことを書く。

## 理由

- `tool_version` は正規化・マッチ規則の版であり（`NormalizerVersion` を含む）、
  質問セットの SHA は何を測ったかそのもの。期間の途中でこれらが変わったときの
  率の差は、サイトの可視性の変化ではなく**測り方の変化**。`compare` は既に
  同じ理由で差を出さないので、`trends` だけが差を出すと 2 基準になる。
- 率と `n` はそのバケットの観測なので消さない（D24 §4 と同じ）。読者は
  「その週は何%だったか」を見られ、前の週との差だけが無い。
- `n_differs` は「同じ測定だが標本数が違う」という別の注意であり、条件の一致を
  前提にする。条件が違うときに `n_differs` を出すと、より重要な理由が隠れる。
- JSON は additive（`identities` と `mixed_conditions` の追加のみ）。既存の
  `reason` / `detail` / `cited_delta` の意味は変えない（D19）。

## 検討した代替案

| 案 | 却下理由 |
|---|---|
| 差を出したまま注記だけ付ける | 表と JSON の数値がそのまま読めるので、注記を読まない読者に「引用率が上がった」と読まれる。compare と語彙も揃わない |
| 条件が変わったら系列を切る（別グループにする） | グループキーは provider × model × stage（不変条件 2）。系列が分断され、利用者が「同じモデルの傾向」を追えなくなる |
| バケットを identity で分割する | 同じ理由。D24 がグループを分割しないと決めている |
| 何もしない（現状） | 期間途中のアップグレードや質問セット変更が、引用率の変化として読めてしまう（issue の指摘） |
| `identities` を出さず `mixed_conditions` だけ出す | 条件が揃っているバケットの identity が読めず、consumer が DB を引き直すことになる。追加は additive で安い |

## 影響

- `aicm trends --json` の各 point に `identities`（配列）と `mixed_conditions`（配列）が増える。
  既存フィールドは不変。
- 条件が変わったバケットの `cited_delta` / `brand_delta` が `null` になり、
  `*_delta_text` が未測定マークになる。以前は数値が出ていた。
- 表の note 列と Markdown レポートの Trend 表に、compare と同じ文言が出る（EN/JA）。
- `n_differs`、`earlier_unmeasured`、`no_history` の挙動は変わらない。
- スキーマ変更なし。`aicm export` の列は変わらない（D19）。
- テスト: `internal/analysis`（条件違い・3 項目・空は absent・混在・順序・既存の n_differs）、
  `internal/reporting`（EN/JA の注記）、`internal/cli`（2 バケットの end-to-end と JSON）。

## 未検証 / 既知の限界

- 検出の粒度はバケット。同じバケットの中で条件が変わり、そのバケットが前の
  バケットと同じ集合になれば、差は出る（集合が同じなら比較可能という compare の
  規則をそのまま使う）。バケット内の混在は `mixed_conditions` で見える。
- 差が無いときの reason は既存の `earlier_unmeasured` のまま（JSON の語彙を変えない）。
  この語は「片側に測定が無い」を表し、現在のバケットが未測定の場合にも使われる。
  reason は変えず、注記だけ「片側が未測定」に直した（以前は「前期間が未測定」と
  断言していた。`docs/metrics.md` にもそう書く）。
- `question_version`（質問文の版）は D24 と同じく identity に含めない。質問文の
  変更は質問セットの SHA が捉える。
- 条件が違うバケットでも率は出る。混在を理由に未測定へ倒さない（D24 §4）。
