# 競合の public suffix を設定検証で拒否し、既存値は消さずに示す（D26）

| 項目 | 内容 |
|---|---|
| 日付 | 2026-11-28 |
| 入力 | issue #11「config: competitors の public suffix を読み込み時に検証する」。D23 で競合行は「サイト」を表すようになったが、`competitors add` 以外の入口（`aicm.toml`、`aicm init --competitors`）は検証していない |
| 前提 | D23（競合引用率は帰属ドメイン単位。§8 で新規登録の拒否まで決定）、D19（スキーマは公開契約）、`CONTRIBUTING.md` の不変条件 1・2 |
| 決定 | `config.Problems()` で `competitors` を検証し、public suffix は **exit 2**。`own_domain` と同じ扱いにする。既存の DB 行は削除せず、`competitors list` が印を付ける |

## 決定

1. **`config.Problems()` が `competitors` の各値を検証する**。public suffix は設定エラー
   （exit 2、`config_incomplete`）にし、メッセージで値と修正方法（registrable domain を登録する）を
   示す。`aicm run` と `aicm init` は既に `Problems()` を通すので、`--competitors` も
   追加のコードパス無しで拒否される。
2. **`own_domain` と同じ扱いにする理由**: どちらも「どのサイトを 1 行にまとめるか」を決める
   計測規則で、D23 以降の競合行はサイト単位（suffix 配下の全顧客を吸い上げる）。
   片方だけ exit 2、片方だけ警告、という差は読者に説明できない。
3. **正規化を 1 か所にする**。`internal/domain` に `CanonicalConfiguredDomain` を置き、
   CLI（`competitors add` / `remove` / `list` / `analysisOptions`）、`run` の runner 設定、
   `config.Problems()` が同じ関数を使う。`https://CO.JP/path` や末尾ドット付きの値も、
   保存・照合・検証・**記録時の `source_type` 分類**で同じドメインとして扱われる。
   runner は run の記録時に分類するので、ここを通さないと URL 形式の競合が
   記録では `other`、レポートでは `competitor` になる。
4. **既存の DB 行は削除しない**。`competitors` 表は管理用の記録であり、計測の入力では
   ない（レポート時に適用される）。`competitors list` は設定と DB の両方を従来どおり
   出し、public suffix の行に
   `public suffix: every site under it would count as this competitor` を付ける。
   `--json` は `{"competitors": {domain: source}, "invalid": [domain, ...]}` を返す。
   `invalid` は該当が無ければ `[]` で、キーは常に存在する（追加のみ。既存の
   `competitors` オブジェクトの形は変えない）。
5. **読み取りコマンドは止めない**。`Problems()` を呼ぶのは `run` と `init` だけで、
   `report` / `trends` / `compare` / `export` / `verify` / `competitors *` は今までどおり
   動く。既存の測定を読めなくするより、新しい計測を始めない方が害が小さい。
   その代わり、設定に残った無効値は直すまでレポートに適用される（§既知の限界）。
6. **警告（stderr の 1 行）にしない**。`report` を含む全コマンドに出すには `LoadConfig` に
   置くことになるが、既定の `run` は進捗出力が多く、警告は見落とされる。`Problems()` は
   もともと「その設定では計測できない」を exit 2 に変換する場所であり、同じ判断を
   2 か所（警告と検証）に置かない方がよい。

## 理由

- D23 で競合の行は「サイト」になった。`co.jp` を登録すると、その配下の無関係な
  サイトがすべて 1 行に集約され、競合引用率も分類も読めなくなる。新規登録だけを
  拒否しても、設定ファイルを手で書けば同じ状態を作れるので、入口を揃える必要がある。
- `competitors add` の拒否（#10）と設定検証（#11）が別の正規化を持つと、
  `https://co.jp/` は片方だけ通る。D23 §2 は「正規化の場所は CLI 境界」と決めたが、
  設定検証が同じ境界を通る以上、関数の置き場所を `domain` に上げるのが実態に合う
  （呼び出し位置は変えない）。
- 既存行を自動削除すると、利用者の管理記録をツールが書き換えることになる。
  表示して直させる方が、何が起きたかが分かる。

## 検討した代替案

| 案 | 却下理由 |
|---|---|
| `LoadConfig` の警告にする | 全コマンドに出せるが、見落とされる。`init --competitors` を拒否するには結局もう 1 か所判断が要る |
| 全コマンドで `Problems()` を呼ぶ（`report` も exit 2） | 設定を直すまで既存の測定を読めなくなる。`own_domain` でも読み取りは止めていない |
| レポート時に無効な競合を黙って無視する | 設定と出力が食い違い、利用者が「登録したはずの行が無い」と読む。行は適用したまま印を付ける方が正直 |
| DB の行を自動削除する | 管理用の表をツールが書き換える。監査できない |
| `Problems()` を分離し、レポート用の検証を別に持つ | 検証が 2 か所になり、exit コードと文言がずれる。今回の範囲では過剰 |

## 影響

- `run` と `init` は、設定に public suffix の競合があると exit 2 で止まる。
  `competitors add` の拒否（#10）と同じ文言の理由を返す。
- `competitors list` の text 出力に印が付く。`--json` に `invalid` が増える（追加のみ）。
- 設定・DB のスキーマ変更なし。`aicm export` の列は変わらない（D19）。
- 正規化関数の移動: `internal/cli` の非公開 `normalizeDomain` を削除し、
  `domain.CanonicalConfiguredDomain` に置き換える（挙動は同じ。テストは `internal/domain` へ移動）。
- テスト: `internal/config`（受理・拒否・重複・正規化）、`internal/domain`（正規化）、
  `internal/cli`（`run --dry-run` の exit 2、`init --competitors` の拒否、`list` の印と
  JSON、行を消さないこと）。

## 未検証 / 既知の限界

- 既存の設定に残った public suffix は、直すまで `report` / `trends` / `compare` で
  引き続き適用される（読み取りを止めない決定の裏返し）。`competitors list` が印を
  付けるので、黙ってはいない。
- `aicm init --force` で設定を書き直すときも検証はかかる。無効な値を残したまま
  `--force` だけを渡すと exit 2 になり、`--competitors` で置き換える必要がある。
- public suffix 一覧は `golang.org/x/net/publicsuffix` に依存する。リストが更新されると
  新たに拒否される値が出るが、既存行は消さない。
- 検証するのは「public suffix かどうか」だけ。`example.com:8080` や空白を含む値など、
  ホスト名でない値は従来どおり受け付けられ、citation のホストと一致しない
  （`own_domain` は `/:` を拒否するが、competitors の一般のホスト検証は別の決定）。
- `run` は DB の既存行が public suffix でも止めない（検証は設定だけ）。それは
  `competitors list` が印を付ける。
