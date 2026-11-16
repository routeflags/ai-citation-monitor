# オーナーによるデバッグ検証（v0.1.2）

この文書は、公開済みの `v0.1.2` を**壊しに行く**ための手順である。CI とテストは実プロバイダーを
呼ばない（鍵もネットワークも使わない）ので、live API の経路はここでしか確認されない。

- 対象: `v0.1.2` の公開バイナリ（この手順はソースツリーに依存しない）
- 前提: 端末で `aicm` が引ける状態。`aicm version` が `0.1.2 (tool_version 0.1.2+norm3)` を返すこと
- 課金: **Phase A は 0 円**。Phase B だけが実際の API 利用料金を伴う。B は明示的に進めるときだけ実行する

## 0. 取得と照合

```bash
mkdir -p ~/tmp/aicm-debug/pkg && cd ~/tmp/aicm-debug/pkg
VERSION=0.1.2
ARCHIVE=ai-citation-monitor_${VERSION}_darwin_arm64.tar.gz   # 環境に合わせる
BASE=https://github.com/routeflags/ai-citation-monitor/releases/download/v${VERSION}
curl -LO "$BASE/$ARCHIVE" && curl -LO "$BASE/checksums.txt"
grep "$ARCHIVE" checksums.txt | shasum -a 256 -c -   # Linux: ... | sha256sum -c -
tar xzf "$ARCHIVE" && mkdir -p ~/.local/bin && install -m 0755 aicm ~/.local/bin/aicm
aicm version
```

展開先には `examples/` と `docs/` も入る（A1 で `examples/questions.yml` を使うので、この場に残す）。

期待: `aicm 0.1.2 (tool_version 0.1.2+norm3)`、`live API: observed once per provider (...)`、
`tests: never call a provider; CI uses fixtures and httptest only`。

## 1. 鍵と作業場所

- 鍵は環境変数で渡す。`aicm` は `.env` を読まない（`.env.example` は変数名の一覧）。
  3 つとも用意済みなら、検証用シェルで `set -a; . ./.env; set +a` のように export する。
- `aicm.toml` には**変数名**しか書かない。鍵の値は絶対に書かない・commit しない。
- 作業は空のディレクトリで行う（`aicm init` が `measurements/aicm.db` と `reports/` を作る）。
  リポジトリ内でやる場合も、この 2 つは `.gitignore` 済み。

```bash
mkdir -p ~/tmp/aicm-debug/site && cd ~/tmp/aicm-debug/site
```

## Phase A — 課金なし（ここだけで大半が壊せる）

### A1. 初期化と質問の取り込み

```bash
aicm init --non-interactive --site-id example-shop --own-domain example.shop \
  --brand-terms "Example" --report-language ja
aicm questions import --file ../pkg/examples/questions.yml --check
```

`--check` は**何も書かずに**入れ子（`smoke ⊆ initial ⊆ regular`）と件数を検算する。
期待（同梱の例）: `stage smoke: 5 questions x 1 run`、`stage initial: 6 questions x 3 run`、
`OK: the question set is usable (nothing was written)`。

`--check` を外して実行すると `questions: inserted=6 ...`、`sha256: ...` が出る。

### A2. 計画の確認（モデル未指定 → exit 2）

```bash
aicm run --provider openai --stage smoke --repeat 1 --dry-run   # exit 2
```

期待: `- openai has no model: set [providers.openai] model in the config or pass --model`。
**モデル名は推測されない**のが仕様。

```bash
aicm run --provider openai --model gpt-6.1-sol --stage smoke --repeat 1 --dry-run
```

期待（`exit 0`）: `auth: openai OPENAI_API_KEY=missing` / `calls: 6 planned, 0 already recorded` /
`condition: stage=smoke repeat=1 session=<今日の日付>` /
`store:     full (answer text and raw JSON are kept and the hash is checked)` /
`dry-run: nothing was sent`。

### A3. 鍵が無い実 run は何も送らない（exit 3）

```bash
env -u OPENAI_API_KEY aicm run --provider openai --model gpt-6.1-sol --stage smoke --repeat 1 --yes
```

期待: `error: no credential for openai (OPENAI_API_KEY); nothing was sent ...`、`exit 3`。
ここで exit 4 や 0 になったら失敗（**送っていない**ことに意味がある）。

### A4. 空の DB でもコマンドが壊れない

```bash
aicm verify          # OK: the database is self-consistent
aicm report --period 30d      # note: no runs in this period; the report says — (not measured)
aicm trends --interval weekly --period 90d   # no runs in this period (—（未測定）)
aicm compare --period 30d --baseline previous  # no comparable groups
aicm export --format jsonl   # 0 行、exit 0
```

`report` を 1 度作ったら、生成された Markdown の冒頭に**標本に関する注意書き**が入っていること、
率の分母 `n` が必ず併記されていること（`n = 0` は `— (not measured)`、`0.0%` ではない）を見る。

### A5. 設定の検出漏れを見る（どれも exit 2、DB も設定も書かれない）

```bash
# 公開サフィックスを own_domain に指定
cd $(mktemp -d) && aicm init --non-interactive --site-id x --own-domain co.uk
# 未対応のレポート言語
cd $(mktemp -d) && aicm init --non-interactive --site-id x --own-domain example.shop --report-language fr
# 既存 aicm.toml を守る
cd $(mktemp -d) && aicm init --non-interactive --site-id x --own-domain example.shop && \
  aicm init --non-interactive --site-id x --own-domain example.shop   # already exists; pass --force
```

### A6. 本文を残さないモード（計画だけ確認）

```bash
aicm run --provider openai --model gpt-6.1-sol --stage smoke --repeat 1 --store hash-only --dry-run
aicm run --provider openai --model gpt-6.1-sol --stage smoke --repeat 1 --store maybe --dry-run   # exit 2
```

期待: 1 つ目は `store:     hash-only (answer text and raw JSON are not written; only the hash is kept)`、
2 つ目は `--store must be full or hash-only`。

## Phase B — 課金あり（明示したときだけ）

1 問 × 1 回 × 1 プロバイダーから始める。live で観測済みのモデルは次の 3 つ。

| プロバイダー | `--model` | 変数 |
|---|---|---|
| OpenAI | `gpt-6.1-sol` | `OPENAI_API_KEY` |
| Gemini | `gemini-3.1-pro-preview` | `GEMINI_API_KEY` |
| Anthropic | `claude-sonnet-5-5` | `ANTHROPIC_API_KEY` |

```bash
# 1 問だけ、stage は使わず --limit 1 で
set -a; . ./.env; set +a
aicm run --provider openai --model gpt-6.1-sol --repeat 1 --limit 1 --yes
aicm verify && aicm report --period 1d
```

3 プロバイダーを回すなら同じ形で `--provider` と `--model` を差し替える。ツールは**料金を計算しない**
（コスト機能は持たない設計）ので、金額は各プロバイダーの料金表で見る。

このフェーズで見たいこと（＝ CI が担保していない部分）:

1. 引用メタデータの形が 3 プロバイダーで揃っているか。`aicm report` の `cited_own` が自社ドメインで 1 になるか
2. Gemini の grounding が**不透明なリダイレクト URL** ではなく、実際の引用元ホストとして正規化されるか
   （`docs/providers.md` の説明どおりか）
3. Anthropic で `max_tokens` 打ち切り（`stop_reason=max_tokens`）がエラー記録になるか
4. 同じ計画の再実行が**スキップ**されるか（`calls: 0 planned, 1 already recorded`）。失敗 run を
   取り直すには `--session <新しいラベル>` が要る
5. `--store hash-only` で計測した DB でも `report` / `export` / `verify` が通るか
6. `Ctrl+C` で中断したとき、それまでの run が残り、`aicm verify` が通るか

## 壊しに行く（すべて Phase A の範囲）

- `aicm.toml` の `own_domain` を消す / `providers.openai.model` を空にする → `exit 2` と、どの項目が悪いかの 1 行
- `--db` で書けないディレクトリを指す → `exit 2`（`cannot open database ...`）
- `aicm.db` を消して `aicm verify` → `exit 2` `project_missing`（空の DB は「未 init」と同じ扱い）。
  `aicm init --non-interactive ... --force` で作り直せば `OK: the database is self-consistent` に戻る
- `--json` を付けたとき、stdout が JSON だけになること（ログは stderr）

## 失敗したときに集めるもの

1. 実行したコマンドと `echo $?`（exit code は 0〜4 で意味が固定されている）
2. stderr の全文（`--verbose` を付けるとリクエスト単位の詳細が出る。鍵は伏字化される）
3. `aicm version --json`（`live_observations` / `live_api_tested` / `endpoint` の版が入る）
4. `aicm verify --json`
5. DB そのものは共有しない。必要なら `aicm export --format jsonl` の該当行と、生レスポンスが要るなら
   該当 run の `responses.raw_json`（**本文と API 応答を含む**ので扱いに注意）

## 片付け

```bash
cd ~ && rm -rf ~/tmp/aicm-debug
unset OPENAI_API_KEY GEMINI_API_KEY ANTHROPIC_API_KEY
```

`measurements/` と `reports/` は `.gitignore` 済みだが、リポジトリ内で試した場合は
`git status` に現れないことを確認しておく。

## 付録: 実サイトでの確認（thch-vape.shop / 「H4CBHとは？」）

実在のサイトと 1 問だけで、live の経路を最短で見るためのセット。`~/tmp/aicm-debug/thch-vape/` に
置いた状態のファイルをそのまま載せる（2026-11-17 作成、`aicm 0.1.2` で `--check` / `--dry-run` 確認済み）。

```bash
mkdir -p ~/tmp/aicm-debug/thch-vape && cd ~/tmp/aicm-debug/thch-vape
aicm init --non-interactive \
  --site-id thch-vape --site-name "リキッド通販ショップ" \
  --own-domain thch-vape.shop \
  --brand-terms "リキッド通販ショップ" --brand-terms "H4CBH" --brand-terms "thch-vape" \
  --market ja-JP --language ja --report-language ja
```

`aicm init` は `subdomains = true` で書き出すので、`www.thch-vape.shop` は自社ドメインとして
数えられる（`own_domain` は登録可能ドメインを書く）。質問セットは `questions.yml`:

```yaml
set_version: "1.0.0"
site_id: thch-vape
own_domain: thch-vape.shop
subdomains: true
market: ja-JP
language: ja

brand_terms_status: proposed
brand_terms:
  - リキッド通販ショップ
  - H4CBH
  - thch-vape
competitors: []

questions:
  - { id: Q01, text: "H4CBHとは？", category: definition }

stages:
  smoke:
    runs_per_question: 1
    question_ids: [Q01]
  initial:
    runs_per_question: 3
    question_ids: [Q01]
```

```bash
aicm questions import --file questions.yml --check   # smoke 1x1 / initial 1x3、何も書かない
aicm questions import --file questions.yml
aicm run --provider openai --model gpt-6.1-sol --stage smoke --repeat 1 --dry-run
```

`--check` は `competitors` が空だと `warning: I042: competitors is empty (the share table shows
own / other only)` を出す。競合を測るなら `aicm competitors add <domain>` で後から足せる
（足すとレポートのシェア表が変わるので、比較する期間の中では変えない）。

**live で見るもの（1 プロバイダー 1 コールから）**

```bash
set -a; . ~/Codes/github.com/routeflags/ai-citation-monitor/.env; set +a
aicm run --provider openai --model gpt-6.1-sol --stage smoke --repeat 1 --yes
aicm verify
aicm report --period 1d
aicm export --format jsonl | head -1   # 1 行目に 1 run の全列が出る
```

- `cited_own`: `H4CBH` の一般的な説明をした回答が `thch-vape.shop` を引用するか。0 でも異常ではない
  （引用メタデータが返っていれば `citation_observability = observed` で分母に入る）
- `citations` の host 一覧: 引用元が実在のドメインか（Gemini で `vertexaisearch.cloud.google.com` が
  残っていたら正規化の不具合）
- `runs.status` / `error_code`: 失敗ならここに出る（401 は `invalid_api_key` で exit 4）
- 同じコマンドの再実行: `calls: 0 planned, 1 already recorded` でスキップされる

**brand_terms の注意**: 質問文自体に `H4CBH` が入っているので、`brand_terms` に `H4CBH` を
入れると `brand_mentioned` はほぼ常に真になる。デバッグでは「言及の抽出が動いているか」を見るには
便利だが、本番の言及率としては意味を持たない。実測用のセットでは質問に出てこない語（店名など）に
絞る。
