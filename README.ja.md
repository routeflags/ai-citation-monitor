# AI Citation Monitor（`aicm`）

![AI Citation Monitor：CLI のタイトルカードと、引用率・ブランド言及・競合比較・トレンドの指標](docs/assets/hero.png)

**生成AIの回答に自社サイトがどれだけ引用されているかを、CLIで再現可能な形で計測するツール。**

<sub>画像はイメージです。図中の数値は計測結果ではありません。</sub>

AI Citation Monitor は、**GEO（Generative Engine Optimization）**と**AEO（Answer Engine Optimization）**の成果を計測する、Go製のオープンソースCLIです。バージョン管理された質問セットを検索機能付きAI APIに送信し、引用の証拠をSQLiteに保存。自社・競合の引用状況や期間別トレンドを分析します。

[English](README.md) · [CLIリファレンス](docs/cli-reference.md) · [指標定義](docs/metrics.md) · [プロバイダー仕様](docs/providers.md)

> [!IMPORTANT]
> **計測結果は標本の観測値であり、AI検索全体の順位ではありません。** 指標が表すのは、設定した質問・プロバイダー・モデル・ステージ・実行回数の範囲だけです。一般ユーザーが利用するChatGPT・Gemini・Claudeのすべての回答を代表するものではありません。生成レポートには、この制約についての注意書きが必ず表示されます。

## このツールでできること

従来のSEO解析だけでは「特定のAI回答が自社サイトを引用したか」を直接確認できません。`aicm` はこれを検証・再集計できるようにします。

- **引用の計測：** 回答本文からURLを推測せず、APIが返す引用メタデータに基づいて判定
- **ブランド・質問別分析：** ブランド言及率、質問ごとの引用率、質問カバー率を集計
- **競合比較：** 同じ計測条件で競合ドメインの引用率・シェアを比較
- **時系列分析：** 期間比較と日次・週次・月次トレンド
- **再現性：** 質問バージョン、モデル、API条件、時刻、回答ハッシュ、必要に応じた生レスポンスを記録
- **自動処理との連携：** JSON出力とCSV/JSON/JSONLエクスポート。Web UIやSaaSへの登録は不要

## インストール

`aicm` は依存のない単体バイナリです（Cコンパイラ・ランタイム不要）。macOS と Linux の amd64/arm64、Windows の amd64 で動作します。

### ビルド済みバイナリ（推奨）

[最新リリース](https://github.com/routeflags/ai-citation-monitor/releases/latest) から、お使いの環境のアーカイブと、並べて公開されている `checksums.txt` をダウンロードします。

| 環境 | アーカイブ |
|---|---|
| macOS（Apple シリコン） | `ai-citation-monitor_<version>_darwin_arm64.tar.gz` |
| macOS（Intel） | `ai-citation-monitor_<version>_darwin_amd64.tar.gz` |
| Linux（x86-64） | `ai-citation-monitor_<version>_linux_amd64.tar.gz` |
| Linux（arm64） | `ai-citation-monitor_<version>_linux_arm64.tar.gz` |
| Windows（x86-64） | `ai-citation-monitor_<version>_windows_amd64.zip` |

実行する前に、公開された checksum と照合してください。

```bash
VERSION=0.2.0                                       # ダウンロードしたリリース
ARCHIVE=ai-citation-monitor_${VERSION}_darwin_arm64.tar.gz   # 自分の環境に合わせる

grep "$ARCHIVE" checksums.txt | shasum -a 256 -c -  # Linux は ... | sha256sum -c -
tar xzf "$ARCHIVE"
mkdir -p ~/.local/bin && install -m 0755 aicm ~/.local/bin/aicm   # または: sudo install -m 0755 aicm /usr/local/bin/aicm
aicm version
```

アーカイブはディレクトリを作らず、`aicm`・`LICENSE`・README・`docs/`・`examples/` を展開します。Windows では `(Get-FileHash .\ai-citation-monitor_0.2.0_windows_amd64.zip -Algorithm SHA256).Hash` を `checksums.txt` の値と照合し、`.zip` を展開してフォルダを `PATH` に追加してください。

### Go で入れる

**Go 1.26.11 以上**（`go.mod` の指定）が必要です。

```bash
go install github.com/routeflags/ai-citation-monitor/cmd/aicm@latest
```

インストール先は `$(go env GOBIN)`、`GOBIN` が未設定なら `$(go env GOPATH)/bin` です。そのディレクトリが `PATH` に入っている必要があります。

### ソースからビルドする

```bash
git clone https://github.com/routeflags/ai-citation-monitor
cd ai-citation-monitor
make aicm     # ./aicm ができる（git のバージョンで刻印。`go build -o aicm ./cmd/aicm` でも可）
make check    # gofmt・vet・build・test・race・lint・govulncheck（CI と同じ検査）
```

Homebrew・apt/dnf・Scoop・コンテナイメージは用意していません。上の 3 つのいずれかを使ってください。

## デモ

セットアップと dry-run の録画です。実 API は呼び出していません。

![aicm のクイックスタート操作デモ](docs/assets/aicm-demo.gif)

## クイックスタート

### 1. プロジェクト作成と質問の登録

```bash
aicm init --non-interactive \
  --site-id example-shop \
  --own-domain example.shop \
  --brand-terms "Example"

aicm questions import --file examples/questions.yml
```

サンプル質問ファイルの `site_id` は `example-shop` です。実際の計測では、ドメイン・ブランド名・質問文を自社用に変更してください。期間比較の際は質問セットのバージョンを管理します。

### 質問ファイル

質問セットは小さな YAML です。動く最小形:

```yaml
set_version: "1.0.0"
site_id: example-shop
own_domain: example.shop
market: ja-JP
language: ja

brand_terms:
  - Example
competitors: []

questions:
  - { id: Q01, text: "Exampleとは？", category: definition }
  - { id: Q02, text: "ExampleとRivalの違いは？", category: comparison }

stages:
  smoke:
    runs_per_question: 1
    question_ids: [Q01]
  initial:
    runs_per_question: 3
    question_ids: [Q01, Q02]
```

`aicm questions import --file questions.yml --check` は検算だけを行い、何も書きません。見るのはステージの入れ子（`smoke` ⊆ `initial`、`regular` があればさらにその下）、`runs_per_question`（1〜5）、ステージごとの往復回数です。`--check` を外すと保存され、`aicm questions list` で中身を確認できます。

- `set_version` はセットの同一性。質問文を変える編集では同じコミットでこれを上げる。上げないと、次回の比較が「引用の変化」に見えてしまう
- `category` は `definition` / `comparison` / `regulation` / `effect` / `safety` / `usage` / `purchase` / `list` / `identification` / `dosage` のいずれか
- `competitors: []` でも動く。その場合 import が `I042` の警告を出し、シェア表は自社 / その他の 2 列になる

### 2. API費用が発生する前に実行計画を確認

```bash
aicm run --provider openai --model YOUR_SUPPORTED_MODEL \
  --stage smoke --repeat 1 --dry-run
```

`--dry-run` はAPIにリクエストを送信せず、APIキーも必要ありません。金額の見積り機能ではないため、利用料金は各プロバイダーの料金表で確認してください。

### 3. 実際に計測する

```bash
export OPENAI_API_KEY="your-api-key"

aicm run --provider openai --model YOUR_SUPPORTED_MODEL \
  --stage smoke --repeat 1 --yes
```

モデルは `--model` または `aicm.toml` で明示する必要があります。**実計測では外部APIの利用料金が発生する可能性があります。** 最初は少数の質問で検証し、必要に応じて `--limit` を使ってください。APIキーを設定ファイルやGitに保存しないでください。

### 4. レポート・比較・検証

```bash
aicm report --period 30d
aicm compare --period 30d --baseline previous
aicm trends --interval weekly --period 90d
aicm verify
```

レポートは設定したディレクトリ（デフォルト `reports/`）に保存されます。集計データの外部利用には `aicm export` を使います。

## レポートの例

保存済みデータから生成したレポートの録画です。実 API は呼び出していません。数値は標本の観測値であり、ベンチマークではありません。

![aicm のレポート表示デモ](docs/assets/aicm-report.gif)

[レポートの静止画](docs/assets/aicm-report.png)

## 質問設計が観測結果を左右する

**2026-11-28**、Perplexity（`openai/gpt-5.6-sol`）、`--store hash-only`、同一条件で 5 問ずつ測った小規模な観測では、質問セットによって自社サイトの引用数が異なりました。

- 量子もつれ・オーロラの原因・オリーブオイルの製法など、一般常識で答えられる質問: **0.0%（n=0/5）**
- 現在の東京の人口・日本の首相・Go の最新安定版・日本のインフレ率・G20 加盟国など、最新情報が必要な質問: **60.0%（n=3/5）**

後者のレポートでは、ドメイン別引用獲得率として自社 `60.0%（n=3/5）`、他ドメイン `20.0%（n=1/5）` などが記録されています。いずれもこの質問セット・モデル・実行条件での観測値です。

この 1 回の小さな標本から、質問を変えれば引用が増えるという因果や、他のプロバイダー・モデルでも同じ結果になるとは言えません。引用として記録できるのは、プロバイダーが検索結果に基づく引用メタデータを返した場合です。**引用 0 件は検索が走らなかった可能性もあり、サイトが検索されたうえで選ばれなかったことを必ずしも意味しません。** 期間を比べるときは質問セットとモデルを固定してください。

## 計測指標

| 指標 | 意味 |
|---|---|
| **GEO引用率** | 引用判定可能な成功回答のうち、自社ドメインを1回以上引用した割合 |
| **ブランド言及率** | 成功回答のうち、登録したブランド語を含む割合 |
| **AEO質問別引用率** | 質問ごとに算出した自社引用率 |
| **AEO質問カバー率** | 引用判定可能な質問のうち、自社引用が1回以上あった質問の割合 |
| **競合引用率** | 引用判定可能な成功回答のうち、競合のサイト（登録したドメイン、サブドメインを含む設定ならその配下も）を引用した割合 |
| **引用シェア** | 対象グループ・分母における各ドメインの引用件数シェア |
| **トレンド** | 時系列と前期間との差（パーセントポイント） |

**数値の読み方：** 率には必ず分母 `n` を表示します。`n = 0` は「未測定」であって「引用率0%」ではありません。失敗したリクエストや引用メタデータの有無を判定できない回答を、単純な「引用なし」に置き換えません。また、呼び出しの**状態**と引用の**観測**は別の次元です。率は**プロバイダー × モデル × ステージ**で分けます。1 つの期間で計測条件の違う記録が同じグループに入る場合は、混在した項目（`mixed_conditions`）を明示し、その平均を 1 つの測定としては提示しません。1つの回答が複数の競合を引用するため、競合引用率の合計は100%を超える場合があります。引用シェアはホスト単位なので、サイト単位の競合引用率とは別の表として読みます。計算式は[指標定義](docs/metrics.md)を参照してください。

## コマンド一覧

| コマンド | 機能 |
|---|---|
| `aicm init` | 設定ファイルとSQLiteの初期化 |
| `aicm questions add\|list\|import` | 質問の追加・一覧・インポート |
| `aicm run` | API計測・事前確認・実行記録 |
| `aicm report` | Markdownレポート生成 |
| `aicm compare` | 期間別比較 |
| `aicm trends` | 日次・週次・月次トレンド |
| `aicm competitors add\|list\|remove` | 競合ドメインの管理 |
| `aicm export` | CSV/JSON/JSONL出力 |
| `aicm verify` | 保存した証拠と参照関係の整合性チェック |
| `aicm version` | バージョンとAPI情報の表示 |

共通フラグ `--json` を使うと標準出力をJSONに統一し、診断メッセージは標準エラー出力に送ります。全オプションと終了コードは[CLIリファレンス](docs/cli-reference.md)を参照してください。

## 対応AIと検証状況

| プロバイダー | API | 検証状況 |
|---|---|---|
| OpenAI | Responses API + `web_search` | **2026-11-15** に1モデル・1質問で実API観測。オフラインのfixtureテストあり |
| Google Gemini | `generateContent` + `google_search` | **2026-11-15** に1モデル・1質問で実API観測。オフラインのfixtureテストあり |
| Anthropic Claude | Messages API + `web_search_20250305` | **2026-11-15** に1モデル・1質問で実API観測。オフラインのfixtureテストあり |
| Perplexity | Agent API + `web_search` | **2026-11-28** に1モデル・1質問で実API観測。オフラインのfixtureテストあり |
| xAI Grok | Responses API + `web_search` | **2026-11-28** に1モデル・1質問で実API観測。オフラインのfixtureテストあり |

上記の実API確認は**特定条件での単発観測**であり、あらゆるモデルの動作や本番計測の完了を意味しません。全質問セットでの実API計測は未実施です。引用抽出、リトライ、レート制限、並列実行、中断・再開についてもテストがあります。詳細は[プロバイダー仕様](docs/providers.md)を参照してください。

## 保存データ・費用・安全性

- **ローカル保存：** SQLiteが原本です。外部DBや専用サーバーは不要です。
- **証拠の保存：** 通常モードは回答本文とAPIの生JSONを保存し、認証情報は除去します。`aicm run --store hash-only` または `store_answer_body = false` で本文と生JSONの保存を省略できますが、後から内容を再確認することはできません。
- **中断・再開：** 同じ質問バージョン・プロバイダー・モデル・反復回数・ステージ・セッションについて、記録済みの呼び出しは再送信しません。**失敗記録も再送信されない**ため、問題解決後に再計測する場合は別の `--session` を指定します。
- **費用管理：** 対話環境では実行確認が必要です。非対話で実行する際は `--yes` を指定します。HTTPリトライによって追加課金が発生する可能性があります。
- **限界：** 引用率は質問設計、言語・地域、モデル・APIの仕様、計測時期に左右されます。検索順位や売上向上を保証する指標ではありません。

## スコープとドキュメント

Phase 1 は **CLI専用**で、OpenAI・Gemini・Anthropic・Perplexity・Grokに対応します。Web UI、サーバー、スケジューラーは対象外です。

- [アーキテクチャ](docs/architecture.md) — コード構造・データフロー・保存方法
- [指標定義](docs/metrics.md) — 各指標の正確な分子と分母
- [プロバイダー仕様](docs/providers.md) — 引用メタデータ・API上の留意点
- [CLIリファレンス](docs/cli-reference.md) — コマンド・フラグ・エラー処理
- [設定例](examples/config.toml)と[質問例](examples/questions.yml)
- [コントリビューション](CONTRIBUTING.md)と[セキュリティ](SECURITY.md)
- [変更履歴](CHANGELOG.md) — リリースノート
- [Phase 1 決定記録](documents/decisions/)

## Philosophy

This column outlines the principles that guide how we engage with OSS. These principles are the reason we continue our day-to-day OSS activities and writing. It reframes long-unreviewed social structures as a form of technical debt. We would be glad if you take a look.

- [Engineering Blog: Society Also Has Technical Debt](https://www.thch-vape.shop/guide/column/git-log--oneline--all--society)

## 関連記事

- [AI SEOの効果測定方法とは？GEO・AEOの引用率を可視化する無料OSSを紹介](https://blog.routeflags.com/2026/11/16/ai-seoの効果測定方法とは？geo・aeoの引用率を可視化す/) — 開発・運営会社の株式会社旗指物による解説記事。

## ライセンス

Apache License 2.0。[LICENSE](LICENSE)を参照してください。
