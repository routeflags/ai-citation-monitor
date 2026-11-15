# ai-citation-monitor（`aicm`）

AI の回答が自社サイトを引用しているかを計測する CLI。引用率・ブランド言及率・質問カバー率・
競合の引用・トレンドを、後から再現できる形で記録する。

> **これは標本についての測定です。** AI サービスは全回答ログを公開していないため、ここに
> 出る率は「この質問セットを・このモデルで・この回数実行した」範囲の観測です。ChatGPT /
> Gemini の一般向け画面や全ユーザーの結果を代表しません。レポートの冒頭にこの固定文が
> 必ず出ます（無効にできません）。

English: [README.md](README.md).

## 状態（Phase 1 / MVP）

この表は意図的に厳しくしている。このリポジトリの検査で確かめたものだけを「検証済み」と書く。

| 領域 | 状態 |
|---|---|
| `init` / `questions add,list,import` / `run --dry-run` / `report` / `compare` / `trends` / `export` / `verify` | **検証済み**（`go test ./...`、オフライン） |
| OpenAI アダプター（Responses API + `web_search`） | **実装済み・実 API 未検証**。形は公開ドキュメントと、採取済み応答の構造を写した fixture に基づく。テストは `httptest` |
| Gemini アダプター（`generateContent` + `google_search`） | **実装済み・実 API 未検証**。同上 |
| Anthropic アダプター（Messages API + `web_search_20250305`） | **実装済み・実 API 未検証**。リクエストと引用の形は公開ドキュメントに基づき、テストはその形を `httptest` で配信する。`max_tokens` の既定は 8192 で、打ち切られた回答はエラーとして記録する |
| 引用メタデータからの抽出 | **検証済み**（cited / not-cited / error / empty / メタデータなし の fixture） |
| リトライ（429/502/503/504・`Retry-After`）・レート制限・並列・Ctrl+C 再開 | **検証済み**（単体テスト） |
| 実 API での計測 | **行っていない**。課金と認証が必要なため、オーナーの明示判断があるときだけ行う |

## 何を測るか

7 つの数字。率には必ず分母 `n` が付く（定義は [docs/metrics.md](docs/metrics.md)）。

1. **GEO 引用率** — 引用判定が可能な成功回答のうち、自社ドメインが 1 回以上引用された割合
2. **ブランド言及率** — 成功回答のうち、登録したブランド語に言及した割合
3. **AEO 質問別獲得率** — 質問ごとの、自社が引用された回答の割合
4. **AEO 質問カバー率** — 引用判定可能な回答が 1 件以上ある質問のうち、自社引用が一度でもあった質問の割合
5. **競合引用獲得率** — ドメイン別に、その回答が引用された割合（合計は 100% を超え得る）
6. **引用シェア** — グループ内の引用総件数に対するドメイン別の割合（分母のスコープを表示）
7. **トレンド** — 日・週・月の系列と、前期間との差（percentage points）

データモデルで強制している 2 つの規則:

- **プロバイダー・モデル・stage をまたいだ合計を出さない**（率はグループ内で閉じる）
- **`n = 0` は `—（未測定）`**。失敗や引用メタデータなしの回答は件数として数え、分母から外す。
  「引用なし」と混同しない

## インストール

Releases にバイナリ（macOS arm64/amd64、Linux arm64/amd64、Windows amd64）を置く。
ソースからは:

```bash
go install github.com/routeflags/ai-citation-monitor/cmd/aicm@latest
```

## 最短の手順

```bash
aicm init --non-interactive --site-id example-shop --own-domain example.shop \
  --brand-terms "Example"

aicm questions import --file examples/questions.yml

# 鍵が無くても計画だけ出せる（認証は別行で表示され、実 run なら exit 3）
aicm run --provider openai --stage smoke --repeat 1 --dry-run

export OPENAI_API_KEY=...          # 設定ファイルには環境変数の「名前」だけを書く

aicm run --provider openai --stage smoke --repeat 1 --yes

aicm report --period 30d
aicm verify
```

## 終了コード

| コード | 意味 |
|---|---|
| 0 | 成功 |
| 1 | 検証に落ちた／一部の計測が失敗した（記録は残る） |
| 2 | 設定・使い方の誤り |
| 3 | 認証が無い（1 回も送っていない） |
| 4 | API・通信の失敗 |

失敗には機械可読な `reason` も付く（`--json` の `error.reason`）。

## 何を保存し、何を保存しないか

- 記録の正は SQLite。1 回の呼び出し = 1 行で、条件（プロバイダー・モデル・API エンドポイント版・
  検索の有効化・測定方法・質問セット版と sha256・開始/完了時刻 UTC）を併記する
- 回答本文と生 JSON は保存する（引用を後から検証できるように）。**認証情報の値は保存しない**
  （アダプターがエラーメッセージと生 JSON から除去する）
- 失敗も記録する。再開時は記録済みの呼び出しをスキップするので、Ctrl+C で二重課金しない
- `aicm verify` が DB 自身の整合性を検査する（回答の欠落、引用の参照切れ、本文とハッシュの不一致等）

## ドキュメント

- [docs/architecture.md](docs/architecture.md)（英語）— パッケージ構成・データフロー・スキーマ
- [docs/metrics.md](docs/metrics.md)（英語）— 各指標の分子と分母
- [docs/providers.md](docs/providers.md)（英語）— エンドポイント・引用メタデータ・リトライ・未検証事項
- [docs/cli-reference.md](docs/cli-reference.md)（英語）— 全コマンドとフラグ
- [documents/decisions](documents/decisions/) — Phase 1 の決定記録（日本語）

## スコープ

- ビルドには Go 1.26 以上。実行は単一バイナリ
- CLI のみ。Web UI・サーバー・外部 DB・定期実行は作らない
- Phase 1 のプロバイダーは OpenAI・Gemini・Anthropic (Claude)（Perplexity は対象外）

## ライセンス

Apache-2.0。[LICENSE](LICENSE) を参照。
