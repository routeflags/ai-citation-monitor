# 引き継ぎ: seo-operations の計測を `aicm` に移す（2026-11-15）

`routeflags/seo-operations` の `scripts/geo/` を独立ツール化した `aicm` が v0.1.1 で公開された。
この文書は、seo-operations 側に移行を判断する人（またはエージェント）へ渡すための要点。
**seo-operations は変更していない。** 移行の判断・実施はそちら側の仕事。

## 確認済みの事実

- 既存の `sites/thch-vape/instrumentation/geo-aeo-questions.yml`（50 問、`set_version 1.1.0`）は
  `aicm questions import --file <path> --check` を**そのまま通った**（`smoke 5×1 / initial 20×3 /
  regular 50×5`、書き込みなしの検証）。
- バイナリ: GitHub Releases `v0.1.1`（macOS arm64/amd64、Linux arm64/amd64、Windows amd64）、
  または `go install github.com/routeflags/ai-citation-monitor/cmd/aicm@latest`。
- OpenAI / Gemini / Anthropic は**各 1 モデル 1 問だけ** live 観測済み。サポートモデルの保証ではない。

## `scripts/geo/` との違い（移行で効く順）

| 論点 | `scripts/geo/`（旧） | `aicm` |
|---|---|---|
| 取得経路 | netloom を subprocess で呼ぶ | 公式 API を `net/http` で直接叩く（SDK なし） |
| 認証 | netloom の env 解決 | `OPENAI_API_KEY` / `GEMINI_API_KEY` / `ANTHROPIC_API_KEY`。設定には変数名だけ |
| 証跡 | カセット（JSONL）+ 応答ファイル | DB の `responses.raw_response_json`（認証情報を除去） |
| 証跡の監査 | カセットの有無を目視 | `aicm verify`（DB 内整合性: responses 欠落・参照切れ・回答ハッシュ不一致） |
| 費用・予算 | なし（旧も価格表なし） | **なし**（D11）。トークン数と web search 回数のみ記録。live の費用はプロバイダー請求で確認 |
| Gemini の引用元 | `web.uri` のホストを採用 | `web.uri` は Vertex のリダイレクト。`web.domain` / `web.title` を引用元として記録（Norm 3） |
| DB | `measurements/geo-aeo.db` | 別スキーマ。旧 DB は読めない（変換が必要） |
| 記録の粒度 | 1 run = 1 行、追記のみ | 1 条件 = 1 行（question version × provider × model × repeat × stage × session）、追記のみ |
| 失敗 run の再試行 | 同じ条件はスキップ | 同じ session ではスキップ。`--session <別ラベル>` で再計測（`aicm run` が注記で案内） |
| レポート名 | `report-<period>.md`（stage が入らず上書き） | period + provider + model + stage + 質問セット版を含む |

## 移行手順（提案）

```bash
# 1) 設定と質問セット（まず --check で確認）
aicm init --non-interactive --site-id thch-vape --own-domain thch-vape.shop \
  --brand-terms "<屋号>" --db measurements/aicm.db
aicm questions import --file sites/thch-vape/instrumentation/geo-aeo-questions.yml --check
aicm questions import --file sites/thch-vape/instrumentation/geo-aeo-questions.yml

# 2) 5 往復だけで旧実装と突き合わせる（smoke 5 問 × 1 回）
aicm run --provider openai --stage smoke --repeat 1 --limit 5 --dry-run
aicm run --provider openai --stage smoke --repeat 1 --limit 5 --yes

# 3) レポートと整合性確認
aicm report --period 30d
aicm verify
```

- 旧 `scripts/geo/` は当面残し、`make geo-*` の呼び替えは**レコードとレポートが一致してから**。
- 移行用のアダプタ（パス解決・seo-operations 側の検証）は seo-operations に置く。`aicm` は
  seo-operations の文書・語彙を import しない。
- **live 実行は課金を伴う**。smoke の 5 往復でも発生するので、オーナーの判断を経てから実行する。

## 失われた 8 run について

旧 `measurements/geo-aeo.db` の 8 run は、カセット 8 本・応答 8 本が既にディスク上に無い。
`aicm verify` の形（DB 内整合性）ならこの状態を検出できるが、**失われた証跡は復元できない**。
過去の 8 run を引き継ぐ場合は「記録だけ引き継ぎ、証跡が無いことを明示する」か、測り直す。
