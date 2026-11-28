# issue #10（コードレビュー）の再検証と実装計画

| 項目 | 内容 |
|---|---|
| 日付 | 2026-11-28 |
| 入力 | GitHub issue #10「AI Citation Monitor — コードレビュー」（routeflags/ai-citation-monitor、2026-11-28 の静的レビュー） |
| 依頼 | 「コードレビューの github issue の実装を product manager へ依頼する」。issue の指摘を**現行 main に対して再検証**し、有効なものだけを直す |
| 対象 | `main` = `df7dd60`（v0.2.0 以降。レビュー時点のコードとは差がある） |
| 制約 | 実プロバイダー API を呼ばない（テストはオフライン）。公開リリース・タグ・push は別途許可があるまで行わない。`CONTRIBUTING.md` の 7 不変条件と D8・D19・D21 を守る |
| 前提決定 | D8（率の分母）、D19（スキーマが公開契約）、D20（ブランド語の状態は来歴）、D21（Lean は算術のみ）、D22（厳密丸め）、`docs/metrics.md` |

---

## 1. 現行コードの再検証（issue の指摘はまだ妥当か）

issue は「2026年11月28日時点の main」を読んだ静的レビューで、テスト・証明・実 API は未実行と
明記している。以下は `df7dd60` の実コードを読んだ結果。

| # | issue の指摘 | 現行の該当箇所 | 判定 | 扱い |
|---|---|---|---|---|
| P1-1 | 競合の引用集計がホスト完全一致で、分類（`ClassifyDomain`）と単位が揃っていない | `internal/analysis/metrics.go` `buildDomains()`（`perRunDomains[run.ID][name]`、`name` は citation の `hostname` そのもの）。`internal/domain/domain.go` `IsOwnDomain` はラベル境界でサブドメインを含む | **有効**（未修正）。登録 `example.com` は `www.example.com` の引用を数えない一方、分類は「競合」と言う。引用シェア表（ホスト単位）と獲得率表（同じくホスト単位、ただし分類はドメイン単位）が同じ表で混ざっている | 修正（D23） |
| P1-2 | 集計キーが provider/model/stage だけで、計測条件の同一性を集計層で保証しない | `internal/analysis/metrics.go` `groupKey()`。`internal/analysis/compare.go` は**期間間**の差だけは条件不一致で拒否する（`conditionMismatch`）。条件は期間単位では条件表に出る（`period.go` `buildConditions` → `markdown.go` `writeConditions`） | **有効（範囲を限定）**。欠けているのは**グループ単位の帰属**と「その率は複数条件の平均である」という明示。`labels.go` の `conditionsNote` は「条件の違う記録は同じ表に入れない」と約束しており、この文言も直す必要がある | 修正（D24） |
| P2-3 | 16MiB 上限を超えた応答を検知できない | `internal/providers/httpclient/client.go` `Do()`: `io.ReadAll(io.LimitReader(resp.Body, 16<<20))`。超過時は切り詰めた本文が成功扱いで返る | **有効**（未修正） | 修正 |
| P2-4 | 失敗 run も `RunsUnknown` に加算され、Error と Unknown が重複する | `internal/analysis/metrics.go` `Build()`（`run.CitationObservability != observed` で加算、status を見ない）、`internal/analysis/period.go` `buildTotals()`、`internal/analysis/trends.go`。既存テスト `TestUnknownObservabilityIsCountedNotDivided` が「error run も unknown に入る」ことを固定している。`docs/metrics.md` には失敗と unknown の表はあるが、**相互排他かどうかを書いていない** | **有効（仕様の未定義）**。数値の不具合ではなく、読者が二重計上と読む曖昧さ | 文書と表示で明示（数値は変えない） |
| P3-5 | `Tenths()` が固定幅 int で、Lean の Nat と差がある | `internal/analysis/metrics.go` `Tenths()`: `1000*n` を `int` で計算。`.goreleaser.yaml` は **amd64 / arm64 のみ**で、32bit バイナリは配布していない。したがって現在の配布物での実害はない。32bit `int` で最初に破綻するのは `n = 2,147,484`（`MaxInt32/1000` の切り上げ） | **有効だが重要度は低**（型の衛生。関数が `int` を受ける以上、幅に依存しない方がよい） | 修正（int64 で計算） |
| — | `proofs/Aggregation.lean` / `Domain.lean` / `Comparability.lean` の追加 | `documents/decisions/20261128-lean-proofs.md`（D21）が「証明するのは算術だけ」「分母の選択・グループ横断の禁止・操作順は Lean の定理にならない」と決定済み。`documents/plan/20261128-lean-verification.md` §1 が各不変条件を棚卸し済み | **対応しない（決定済み）**。ただし review が挙げた不変条件のうち**テストで担保できるもの**（集計の順序非依存、引用数 ≤ 判定可能数、失敗を分母に入れない、サブドメイン包含の一貫性）は Go のテストで固定する | 却下（理由を issue に記録）＋テスト追加 |

### 再検証で分かった「issue に無い」副次的な不具合

- `buildDomains()` の登録競合は `strings.ToLower(strings.TrimSpace(...))` だけで正規化している。
  末尾ドット付き（`Example.COM.`）や IDN を登録すると、正規化済みホストと一致しない。
  同じ「集計単位」の不具合なので P1-1 の中で直す（正規化の場所は CLI 境界と `domain` の両方。
  `cli/root.go` `analysisOptions` と `cli/competitors.go` `normalizeDomain` が別々の正規化を持っている）。
- `compare` は期間間で `api_endpoint_version` / `question_set_sha256` / `tool_version` を見るが、
  `trends` はバケット間の条件を見ない。issue #10 の範囲外なので別 issue にする。
- **`trends` の分母の不具合**（P2-4 の隣）: `Trends` は `CitedOwn` を
  `NewRate(entry.cited, entry.valid-entry.unknown)` で作る。`unknown` は status 非依存で
  失敗 run も含むのに `valid` は成功 run だけなので、成功 run 1 件（unknown）＋失敗 1 件で
  分母が負になり、率が「未測定」に化ける。P2-4 と同じ「unknown の数え方」の話なので
  今回直す（success ∧ observed を别カウンタで数える）。
- `labels.go` の `conditionsNote` は「records with different conditions are never put in the
  same table」と断言している。D24 は混在を許して明示するので、この文言も直す。
- 競合に public suffix（`github.io`、`co.jp`）を登録できる。従来は分類だけが over-match したが、
  D23 の帰属ドメインでは**行そのもの**が全顧客を吸い上げる。`competitors add` で拒否し、
  既存の設定/DB にある値は既知の限界として文書化する（設定検証は別 issue）。

---

## 2. 決定事項（新規に記録する）

### D23 競合引用率は「帰属ドメイン」単位にする

- 行の単位: `subdomains = true` のときは**帰属ドメイン**（= そのホストを含む、設定済みの自社/競合ドメインのうち最も具体的なもの。無ければ registrable domain = eTLD+1）。`subdomains = false` のときは従来どおりホスト完全一致（registrable への集約はしない）。
- `Runs`（分子）: その帰属ドメイン配下のホストを1つ以上引用した judgeable run の数。分母は従来どおりグループの judgeable run。
- 引用シェア表は**ホスト単位のまま**（分母はグループの引用総件数）。2つの表は別の指標であり、単位が違うことを docs と report に書く。
- 分類は行のキーに対して `ClassifyDomain` を適用する。キーは「設定ドメイン」か「registrable domain」なので、`own` / `competitor` / `other` が引用元ホストの分類と一致する（設定が registrable より深い場合はその深いドメインが行になるため、他人のホストを巻き込まない）。

### D24 集計層に計測同一性を持たせ、混在を明示する（率は拒否しない）

- `analysis.MeasurementIdentity` = `api_endpoint_version` / `tool_version` / `question_set_version` / `question_set_sha256`。`groupKey` は変えない（グループ = provider × model × stage は不変条件 #2）。
- `Group` は自分に入った identity の集合と、**混在している項目**（JSON `mixed_conditions`）を持つ。混在があれば report の条件節がグループ名付きで警告行を出す（GEO の表はグループごとの表ではないため）。率は今までどおり計算する（数値を黙って変えない）が、**1つの測定ではない**ことを読者に明示する。
- `compare` は同じ型と項目名を使うが、比較する項目は従来の 3 つ（endpoint / question_set_sha256 / tool）のまま。空の値の比較規則も従来どおり。期間間の差を出さない規則を変えない（D24）。

---

## 3. BDD タスク一覧

各タスクは「振る舞い（入力 → 期待）」の単位。正常系・異常系・エッジを含む。

### T1. 競合引用率を帰属ドメインで数える（P1-1 / D23）

**振る舞い**

1. `subdomains = true`、競合 `example.com`、judgeable run 3件の引用が `example.com` / `www.example.com` / `blog.example.com` → `example.com` 行は `Runs=3`, `Rate=100.0% (n=3/3)`、分類 `competitor`、`Registered=true`。
2. 同じ入力で `subdomains = false` → `example.com` 行は `Runs=1`（`www.` と `blog.` は別ホスト行、registrable に集約しない）、`www.example.com` 行は `Runs=1` の観測のみ、`www.example.com` の分類は `other`。
3. 競合が未引用 → `Runs=0`, `n=0/3`（`— (not measured)` ではなく 0 を分子に持つ率）。
4. 登録値の正規化: `Example.COM.` を登録 → ホスト `example.com` に一致する（末尾ドット・大文字・空白）。IDN 登録（`例え.テスト`）は punycode ホスト（`xn--r8jz45g.xn--zckzah`）に一致する。
5. 引用シェア表はホスト単位: `www.example.com` と `example.com` は別行、分母はグループの引用総件数。
6. 分類の一貫性: 帰属ドメインの行の `Classification` は、そのドメイン配下のホストの分類と一致する（自社ドメインのサブドメインは `own`、`notexample.com` は `example.com` に一致しない）。

**異常系・エッジ**

7. registrable domain が取れないホスト（`localhost`、IP アドレス、`github.io` のような public suffix 自体）→ そのホスト自身を行のキーにする（捨てない）。
8. 競合登録が registrable より深い（`blog.example.com`）→ 行は `blog.example.com`。同じ registrable の `shop.example.com` は `example.com` 行に残り、**混ざらない**。
9. 自社ドメインが registrable より深い（`shop.example.com`）→ `shop.example.com` は `own`、同じ registrable の `www.example.com` は `other`。行が混ざらない。
10. `(no host)`（malformed URL）→ 競合獲得率には出ない（シェア表にだけ 1 件として出る）。
11. 同じ run が同じ帰属ドメインの複数ホストを引用 → `Runs` は 1（run 単位で重複しない）。
12. 同じ帰属ドメインの複数 run が同一ホストを引用 → 引用の本数ではなく run の数で数える。
13. 自社と競合が同じドメインに解決する設定（own = `example.com`, competitor = `example.com`）→ 行は 1 つ、分類は `own`（`ClassifyDomain` の順序）。

**「Domain.lean」の代替（D21 に従い Go テストで担保）**: 7〜10 がサブドメイン包含の一貫性のテストになる。

### T2. 計測同一性と混在の明示（P1-2 / D24）

**振る舞い**

1. 同じグループで `tool_version` が 2 種類 → `Group.MixedConditions` に `tool_version: a,b` が入り、率は今までどおり計算される（`CitedOwn` の n は全 judgeable run）。
2. 同じグループで `question_set_sha256` が 2 種類 → 同様に混在として出る。`question_set_version` も独立に見る。
3. 条件が揃っている → `MixedConditions` は空。report に警告行は出ない（既存の出力が変わらない）。
4. report（EN/JA）: 条件節に、混在グループごとに「この率は複数条件の平均であり 1 つの測定ではない」旨の行が出る（グループ名付き）。
5. `compare` は同じ `MeasurementIdentity` 型を使い、期間間の条件不一致で差を出さない。比較する 3 項目と空値の扱いは従来のまま（既存テストの維持）。
6. **順序非依存**（review の Comparability「入力順序が集計結果に影響しない」）: runs / citations を並べ替えても `Build` の出力（グループ順・各行）が完全に一致する。同じ question key の表示メタデータ（text/category）は最初の run が採用される既存仕様なので、その入力を同一にした範囲で検証する。
7. **集計の不変条件**（review の Aggregation の代替）: どのグループでも `0 <= N <= D`、`Σ PerQuestion.D == CitedOwn.D`、`Σ DomainShare.Count == CitationTotal`、失敗 run は `CitedOwn.D` に入らない。

**異常系・エッジ**

8. 質問セット未記録の run（ad-hoc CSV、`question_set_sha256 = ""`）と同じグループに記録済み run → 混在として出る（空も 1 つの条件として数える）。
9. identity の並びは決定的（ソート済み）で、入力順に依存しない。

### T3. HTTP 応答上限の検知（P2-3）

1. 本文が上限ちょうど（16MiB）→ 従来どおり成功。
2. 本文が上限+1 → エラー。メッセージは「上限を超えた」と上限値を名指しし（実際のサイズは上限+1 までしか読まないので「これ以上」と表現する）、部分 JSON をアダプターに渡さない。
3. 上限超過は**再試行しない**（`MaxRetries=2` でもリクエストは 1 回）。ただし 429/5xx の再試行判定を**先**に行う（再試行可能なステータスの本文が大きいという理由で再試行を落とさない）。
4. 本文が上限内で読める → 既存の 429/5xx 再試行は変わらない。
5. 読み取りエラー（接続切断）→ 従来どおり再試行対象。
6. エラー種別は従来の transport のまま（呼び出しが完了していないため）。メッセージでサイズ上限とわかるようにする。

### T4. Error と Unknown の関係を定義する（P2-4）

1. `docs/metrics.md` に「状態（status）と観測（observability）は別の次元」を明記: `success + error + cancelled = runs_total`、`runs_unknown` は status を問わず観測が `observed` でない run、したがって失敗 run は `error` と `unknown` の両方に出る。引用率の分母は `success ∧ observed`。
2. report の表示語を直す: 「引用メタデータなし（unknown）」は失敗 run にも掛かるため誤解を招く → EN `citation metadata not observed (unknown)` / JA `引用メタデータ未観測（unknown）`。`observedNote` と `limits` の該当行も同じ語に揃える。
3. テストで固定: 失敗 run が `RunsError` と `RunsUnknown` の両方に数えられること、`success+error+cancelled == RunsTotal` であること（グループと合計の両方）。
4. **`trends` の分母を直す**: `NewRate(entry.cited, entry.valid-entry.unknown)` は status 非依存の `unknown` を成功数の `valid` から引くため、成功 run 1 件（unknown）＋失敗 1 件で分母が負になる。success ∧ observed を専用カウンタで数えて `NewRate(entry.cited, judged)` にする。テストで固定する。
5. 数値・JSON フィールド名は変えない（契約維持。`trends` の修正は誤った分母を正すもの）。

### T5. `Tenths()` を int64 で計算する（P3-5）

1. `n = d = 3_000_000` → `100.0%`、`n = 2_500_000, d = 3_000_000` → `83.3%`（どちらも 32bit `int` のとき `1000*n` が溢れる領域）。ただし配布は amd64/arm64 のみなので、これは将来の型の衛生であり、配布物の不具合ではない。
2. 既存の厳密丸めテスト（`d <= 300`, `n <= d` の全数）と `proofs/rate-vectors.csv` の再現が変わらない。
3. Lean のモデル（`Nat`）と `rate-vectors.csv` は変更不要。Go 側の上限（int64 で `1000*n` が正確に表せる範囲）を doc コメントに書く。
4. 32bit 向けにコンパイルできる（`GOARCH=arm go test -c`）。これはコンパイル検査であり、32bit 実行ではない。

### T6. 文書・決定記録

1. `documents/decisions/20261128-competitor-attribution-domain.md`（D23）。
2. `documents/decisions/20261128-measurement-identity.md`（D24）。
3. `docs/metrics.md`: 競合引用率の単位、シェアとの違い、status と observability の次元、混在の扱い。
4. `docs/architecture.md`: 集計層の identity と「混在は隠さない」規則。
5. `README.md` / `README.ja.md`: 競合引用率の説明を、行が「登録ドメインか registrable domain」であることに合わせる（同しコミット）。
6. `docs/cli-reference.md`: `report --json` のフィールドに `mixed_conditions` を追記。
7. issue #10 に検証結果（有効/修正/却下）と検証コマンドをコメント。

### T7. 検証

- `gofmt -l .`（空）、`go vet ./...`、`go build ./...`
- `go test ./...`、`go test -race ./...`（C ツールチェーンがある場合）
- `golangci-lint run`（無ければ未実行として報告）
- `GOARCH=arm go test -c ./internal/analysis`（32bit の型検査）
- `make proof`（Lean が無い環境では未実行として報告。モデルとベクタは変更しない）

---

## 4. 変更するファイル（予定）

- `internal/domain/domain.go` + `domain_test.go`: `RegistrableDomain`（eTLD+1、失敗時はホスト）と `AggregationDomain`（設定ドメイン優先）。
- `internal/analysis/metrics.go` + `metrics_test.go`: `buildDomains` の帰属ドメイン集計、`MeasurementIdentity`、`Group.MixedConditions`、`Tenths` の int64 化、unknown のコメント。
- `internal/analysis/compare.go`: 条件比較を `MeasurementIdentity` に寄せる（比較する 3 項目・空値の扱いは不変）。
- `internal/analysis/trends.go`: `CitedOwn` の分母を judged 件数に直す（P2-4 の隣の実バグ）。
- `internal/analysis/period.go`: `buildTotals` のコメント（不変条件）。
- `internal/providers/httpclient/client.go` + `client_test.go`: 上限検知（再試行判定の後）。
- `internal/reporting/markdown.go` + `markdown_test.go` + `labels.go`: 混在の警告行（条件節）、`conditionsNote` の言い直し、unknown の表示語。
- `internal/cli/cli_test.go`、`internal/cli/competitors.go`（public suffix の拒否と正規化）、`internal/cli/root.go`（`analysisOptions` の正規化）。
- `docs/metrics.md`, `docs/architecture.md`, `docs/cli-reference.md`, `README.md`, `README.ja.md`。
- `documents/decisions/`（D23・D24）、この計画書。

## 5. リスクと対応

| リスク | 対応 |
|---|---|
| 競合獲得率の `domain` の値が変わる（`www.rival.example` → `rival.example`）。レポートの見た目が変わる | 決定記録 D23 に書く。シェア表（ホスト単位）は変えない。CHANGELOG はリリース時に書く（今回はリリースしない） |
| 混在の警告でレポートがうるさくなる | 条件が揃っている通常運用では 1 行も増えない。混在は「読者が知るべき」状態なので出す |
| `AggregationDomain` の設定優先ルールが複雑 | テスト T1-6〜10 で固定。docs に 1 段落で書く |
| 16MiB のテストが遅い | `httptest` で 16MiB を 2 回書くだけ。オフラインで数百 ms |
| int64 化で丸めが変わる | 計算式は同じ。既存の全数テストとベクタで確認 |
| 競合に public suffix（`co.jp` など）を登録すると、D23 でその配下の全サイトが 1 行に集約される（分類は従来から over-match していた） | `competitors add` で拒否する（新規登録）。既存の設定/DB に残る値は既知の限界として D23 と architecture に書き、設定検証は別 issue。 |

## 6. 対象外

- `trends` のバケット間の**条件**比較（分母の不具合は今回直すが、条件が違うバケットの差を出さない規則は別 issue）。
- 設定ファイルの `competitors` の public suffix 検証（`competitors add` の拒否だけ今回入れる）。
- Lean の追加証明（D21 で却下済み）。
- スキーマ変更、リリース、タグ、push。
- issue #10 の P1/P2/P3 以外の一般的な感想（アーキテクチャ評価の表）。

## 7. Plan Architect レビューの反映

計画はサブエージェント（scout、読み取り専用）に現行コードと照合させた。BLOCK だった指摘への対応:

| 指摘 | 対応 |
|---|---|
| P3-5 の証拠が誤り（32bit 配布は無い） | 重要度を「型の衛生」に下げた。破綻点は `n = 2,147,484`。テストは 64bit でも通る（幅ではなく規則を見る）ことを明記 |
| P1-2 の「どこにも出ない」が誤り（期間単位の条件表はある） | 欠けているのはグループ単位の帰属と「平均である」旨に限定。`conditionsNote` の約束も直す対象に追加 |
| D24 のフィールド名が 3 通り | JSON は `mixed_conditions` に統一 |
| `compare` の 3 項目 / 空値無視を変えてしまう | 型と項目名だけ共有し、比較項目と空値の扱いは不変 |
| 上限超過を再試行判定より先に返すと 429/5xx の再試行が落ちる | 再試行判定を先にした。メッセージは「上限より大きい」と表現（上限+1 までしか読まない） |
| D23 の正規化の場所が違う | `analysisOptions` と `competitors add`（CLI 境界）で正規化し、`AggregationDomain` も防御的に正規化 |
| D23 が public suffix の危険を拡大する | `competitors add` で拒否。既存の設定値は既知の限界として文書化（設定検証は別 issue） |
| `trends` の分母の実バグ | 今回直す（judged カウンタ） |
| T2-6 は同一 question key の text が違うと成立しない | 不変条件の範囲を「表示メタデータが同一」に限定 |
| T2-4 の警告の置き場所 | GEO 表はグループ別ではないので条件節に置く |
| README の表現 | 行が「登録ドメインか registrable domain」であることに合わせる |
| `documents/plan/20261128-lean-verification.md` が無い | **誤り**。実在する（§1 の不変条件表と I8 を引用している）。この指摘は採用しない |

## 8. 実施結果（この計画のコミット時点）

- P1-1: `domain.RegistrableDomain` / `domain.AggregationDomain` を追加し、`buildDomains` の獲得率を帰属ドメインで数える。テスト: サブドメイン集計、exact-match、未引用登録、深い登録の非過剰一致、正規化。
- P1-2: `MeasurementIdentity` / `MixedConditions`、`Group.MixedConditions`、条件節の警告行、`compare` の型共有。テスト: 混在検出、単一条件は無警告、順序非依存、集計不変条件。
- P2-3: `maxBodyBytes+1` の読み取りと `ErrResponseTooLarge`。テスト: 上限ちょうど、上限+1、再試行しない、429/5xx の再試行が優先。
- P2-4: `docs/metrics.md` の次元の定義、レポートの語（EN/JA）、`trends` の分母修正。テスト: 二重計上を仕様として固定、trends の judged 分母。
- P3-5: `Tenths` の int64 化。テスト: 大きな件数、既存の全数テストとベクタ。
- 検証: 下のチェックリストを実行し、結果を issue #10 のコメントに残す。

### コードレビュー（実装後）の指摘と対応

実装を別のサブエージェント（scout、読み取り専用）に `df7dd60..HEAD` でレビューさせた。APPROVE WITH CHANGES。ブロッカー 2 件は追加コミット `b3e7a4c` で修正した。

| 指摘 | 対応 |
|---|---|
| `analysisOptions` と `competitors add` で正規化が違う（config の URL が「一致しない競合行」になる） | 両方を `normalizeDomain` に統一。単体テストを追加 |
| レポートの `competitorNote` / `shareNote` が、獲得率（サイト単位）とシェア（ホスト単位）の単位差を説明していない | EN/JA の注記を更新し、テストで固定 |
| `registered` が `own` 行で true になる | D23 §7 どおり `own` は `registered = false` に。テスト追加 |
| 旧バージョンが保存した Unicode の競合行を `remove` できない | 保存値の正規化形が一致する行も削除。`list` はソート済みの正規名 |
| `docs/architecture.md` の compare の説明が 4 項目に読める | 「4 のうち 3 項目、空は差分としない」と明記 |
| `docs/cli-reference.md` の「per site」が `subdomains = false` を無視 | 但し書きを追加 |
| `buildTotals` のコメントが計画にあるのに無い | コメントを追加 |

### 最終検証（ローカル、push なし）

- `gofmt -l .` → 空
- `go vet ./...` / `go build ./...` → 成功
- `go test -count=1 ./...` → 全パッケージ ok
- `go test -race -count=1 ./...` → 全パッケージ ok
- `golangci-lint v2.15.0 run` → `0 issues.`
- `GOOS=linux GOARCH=arm go test -c ./internal/analysis` → コンパイル成功（実行はしていない）
- `make proof`（Lean）→ **未実行**。この環境に `lean`/`elan` が無い。`proofs/Rate.lean` と `rate-vectors.csv` は変更していない（`Tenths` の丸め規則は不変で、`internal/analysis/vectors_test.go` は `go test` で通っている）
- `govulncheck` → 未実行（ネットワーク取得が必要なため。CI の `make check` の範囲）

