# 競合引用率の集計単位を「帰属ドメイン」にする（D23）

| 項目 | 内容 |
|---|---|
| 日付 | 2026-11-28 |
| 入力 | issue #10（コードレビュー）P1-1「競合ドメインの集計単位が不統一」。`internal/analysis` `buildDomains()` はホスト完全一致で数える一方、`internal/domain` `ClassifyDomain()` はラベル境界でサブドメインを含む |
| 前提 | D8（率の分母は 1 か所）、D19（スキーマが公開契約）、`CONTRIBUTING.md` の不変条件（率は n を持つ／グループをまたいで合算しない） |
| 決定 | 競合引用率の行は**帰属ドメイン**（設定ドメインか registrable domain）で数える。引用シェア表はホスト単位のまま。率・分母の定義（D8）は変えない |

## 決定

1. **行のキー（帰属ドメイン）** を 1 つの関数で決める。
   - `subdomains = false`: キーは**ホストそのもの**（従来の完全一致を保つ。registrable への集約はしない）。
   - `subdomains = true`: ホストを含む**設定済みドメインのうち最も具体的なもの**（`own_domain` または `competitors`。ラベル境界で判定）。
     該当が無ければ **registrable domain（eTLD+1）**。`publicsuffix.EffectiveTLDPlusOne` が
     失敗するホスト（`localhost`、IP アドレス、`github.io` のような public suffix 自体）は
     ホスト自身をキーにする（引用を捨てない）。
2. **設定値の正規化**。正規化の場所は **CLI 境界**（`analysisOptions` と `competitors add`）とし、`domain.CanonicalHost`（小文字・末尾ドット除去・punycode）を使う。従来は `strings.ToLower(TrimSpace())` だけで、`Example.COM.` や IDN の登録が正規化済みホストと一致しなかった。`AggregationDomain` も防御的に正規化する（DB に古い値が残っていても集計が割れない）。
3. **分子は run の数**。そのキーに属するホストを 1 つ以上引用した judgeable run を数える。
   同じ run が同じキーの複数ホストを引用しても 1。
4. **分母はグループの judgeable run**（D8 のまま）。`subdomains = false` でも true でも同じ。
5. **引用シェア表は変えない**。シェアはホスト単位で、分母はグループの引用総件数（malformed も 1 件）。
   競合獲得率（run あたりの率）とシェア（引用あたりの率）は別の指標であり、単位が違うことを
   `docs/metrics.md` とレポートの注記に書く。
6. **分類はキーに対して `ClassifyDomain` を適用**する。自社を先に見る順序は変えない。
   キーは「設定ドメイン」か「registrable domain」なので、設定が registrable より深い場合
   （`blog.example.com` を競合登録）でも、その深いドメインが行になり、同じ registrable の
   別ホスト（`shop.example.com`）を巻き込まない。逆に `shop.example.com` を自社登録した場合、
   同じ registrable の `www.example.com` は `other` の行に残る。
7. **`registered` フラグ**は「競合として登録されている」の意味（従来どおり）。自社ドメインの行は
   `registered = false` のまま（`classification = own` で読み分ける）。未引用の登録競合は
   `Runs = 0` の行として出る。
8. **public suffix の競合登録は新規には拒否する**。`competitors add` は `domain.IsPublicSuffix` で
   `github.io` / `co.jp` をエラー（exit 2）にする。D23 では行そのものが suffix 配下の全サイトを
   吸い上げるため、分類だけの over-match より影響が大きい。既存の設定ファイル/DB に残る値は
   引き続き over-match する（既知の限界として `docs/architecture.md` に残し、設定検証は別 issue）。

## 理由

- レビューの指摘どおり、同じ表の中で「ホスト完全一致の獲得率」と「サブドメインを含む分類」が
  混ざっていた。登録 `example.com` が `www.example.com` の引用を 0 件と表示する一方、
  分類は `competitor` と言う状態は、数字の意味が読めない。
- 帰属ドメインを 1 つの関数で決めることで、行の名前・分子・分類・registered が同じ規則に従う。
- シェア表をホストのまま残すのは、**引用の分布**（どのホストが引用されたか）と
  **回答あたりの獲得率**（そのドメインを引用した回答の割合）が別の問いだから。
  2 つの分母を同じ表に混ぜない（`docs/metrics.md` の既存の注意と同じ）。

## 検討した代替案

| 案 | 却下理由 |
|---|---|
| 行はホストのまま、登録ドメインの行だけサブドメインを数える（`IsOwnDomain(host,row,subdomains)` の 1 行修正） | 件数は直るが、同じ run が `example.com` と `www.example.com` の両行に数えられ、表が二重計上に見える。集計単位が 1 つの表に混在する |
| `subdomains = false` でも registrable に集約する | 利用者が「完全一致のみ」を選んでいる意味を壊す（`--subdomains` は分類だけでなく行の単位の指定でもある） |
| 獲得率の行を設定競合だけにし、観測ホストはシェア表だけにする | 未登録ドメインの獲得率（現行の価値）を失う。挙動変更が大きすぎる |

## 影響

- レポートの「ドメイン別 引用獲得率」の `domain` 列の値が変わる（`www.rival.example` → `rival.example`）。
  行数は減り得る。`aicm report --json` の `groups[].competitor_rate[].domain` も同じ。
- スキーマ変更なし。`aicm export` の列は変わらない（D19）。
- 引用シェア表、`cited_own`、質問別・カバー率・トレンドは変わらない。
- テスト: 帰属キーの単体（`internal/domain`）、`buildDomains` の獲得率（`internal/analysis`）、
  CLI の end-to-end（`internal/cli`）。
