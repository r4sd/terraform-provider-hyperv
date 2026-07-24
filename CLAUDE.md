# terraform-provider-hyperv — Claude Code 作業規約

## プロジェクト文脈

taliesins/terraform-provider-hyperv の soft-fork(module path は taliesins のまま、homelab は
`dev_overrides` でローカル参照)。PowerShell 依存を [go-wsman](https://github.com/r4sd/go-wsman)
(Go ネイティブ WS-Man/CIM クライアント)へ段階移行している。位置づけ=**ほぼ自分用 + 学習**の
趣味プロジェクト(使命感・完遂ノルマなし)。実運用 provider が要る時の選択肢は windsorcli/hyperv。

- 移行の現況(living doc): Obsidian `20-Projects/terraform-provider-hyperv/migration-status.md`
- go-wsman 側のコーディング規約: go-wsman リポジトリの `CLAUDE.md`(CIM 命名・MOF 一次資料検証・
  TDD サイクル・golden 規約)。**このファイルは主に provider 側 shadow 移行の DoD を定義する。**

## feature flag と strict モード

- `HYPERV_USE_WSMAN=1`: go-wsman 経由に切替(未移行メソッドは埋め込み `hyperv_winrm.ClientConfig`
  から promotion で PS フォールバック)。
- `HYPERV_WSMAN_STRICT=1`: PS フォールバックを fail-fast スタブ(`StrictNoPSClient`)に差し替え、
  PS 呼び出しが 1 件でも走ったら即エラー。v2.0「Home-env PS-free」の陽性証明手段。

---

# シャドウ移行 DoD(Definition of Done)

PS 依存を go-wsman へ移す **1 機能 = 1 縦スライス**の「完了の定義」。バイブコーディング防止の
framework。Slice A(vm_processor write)を型として蒸留(2026-07-19)。**新しい機能を go-wsman 化
する時は、以下を上から順に満たす。**

## 1. 縦スライスのパイプライン(順序固定)

```
① 一次資料(MOF)で名前・型・単位・列挙値を確認 → testdata/mof に fixture 化
② go-wsman primitive(無ければ TDD: golden→RED→GREEN で実装 → merge → provider を @main に bump)
③ provider shadow(read 変換 / write 逆変換 + ガード)
④ unit test(純関数: 変換・ガード・境界)
⑤ 実機 acc test(//go:build integration。mutation は使い捨て VM を CreateVm→t.Cleanup で削除)
⑥ Fable 敵対レビュー → 致命指摘を反映 → 実機で再検証
⑦ PR → CI 全 green
```

## 2. Hard gate(機械強制 = ハーネス。CI が落とす、Claude の記憶に依存しない)

- [ ] `go build ./...` / `go vet` / `go test ./...`(unit)green
- [ ] golangci-lint(**gosec G115** 符号変換含む)/ CodeQL / govulncheck / gofmt green
- [ ] go-wsman に新 CIM クラスを足したら `cim_compliance_test`(struct の `cim:` タグ ↔ MOF fixture)に登録

## 3. Soft rule(規約 = Claude が守る。DoD で明文化して「守ったか」を必須確認項目にする)

- [ ] **一次資料検証**: golden / 型は Microsoft MOF 由来。Issue 記述・他言語ライブラリは二次情報。
      **型に合わせた golden の自作は禁止**(`feedback_external_api_spec_verification` (memory))。
- [ ] **TDD 先行**: RED を確認してから実装(`feedback_tdd_first_default` (memory))。
- [ ] コメントは **WHY**(単位・契約・非自明な制約)。コードを言い換える WHAT は書かない。
- [ ] 実機 acc test を **1 回は実行して GREEN を確認**(CI 非実行のためログを PR に残す)。
- [ ] **Fable 敵対レビュー**を実施し、致命指摘は merge 前に反映。

## 4. この型が固めた shadow 特有の設計ルール(再発防止)

| ルール | 由来(Slice A / strict で実証) |
|--------|-------------------------------|
| 単位変換は read⇄write を**厳密に逆**にし **round-trip unit test** で固定する | Maximum⇄Limit×1000 |
| **PS-0 のための省略ガード**(空 or 差分なしで書き込み 0 件)で homelab 既定運用を PS-free 化 | GPU 空ガード / processor 差分なしガード |
| **"0=Dynamic" フィールドは現行値へ正規化**(CIM は解決済み実値を返す)。しないと恒常 diff / 毎回 Set | NUMA=12 を実機確認、正規化なしで既定 config が毎回 Set(Fable F-1) |
| **go-wsman で表現できない変更は PS 委譲**。非ゼロ→0/false は `marshalEmbeddedInstance` がゼロ値を送らず**黙殺**される。**黙って成功報告する実装は禁止**(検出 → PS フォールバック or 明示エラー) | Reserve 10→0 のリグレッション(Fable C) |
| **strict / PS-0 テストは negative control 必須**(既知の PS 経路が発火することを示す)。**恒真アサーション禁止** | wait_for_ips=true 発火 / GetVmFirmwares 発火 |
| shadow メソッドは promotion と区別して検証する(`runtime.FuncForPC().FileLine()` で定義元ファイルを確認) | `assertShadowedIn`(Fable のトートロジー指摘反映) |

## 5. Definition of Done

**§2 + §3 の全項目 ✓ かつ PR CI 全 green。** 上位ゴール(例: full-lifecycle PS-0)に未達がある
場合は、残る部分を**正確にスコープ明記**する(どの field / VM 世代 / 条件が残 PS か)。「達成した」
と曖昧に丸めない。

---

## リポジトリ運用メモ

- 個人リポ(private)。commit/push/PR 可。merge は原則ユーザー判断を仰ぐ(スコープ確認が要る PR は特に)。
- コミットは Conventional Commits(prefix 英語、説明日本語可)。PR 本文に工数・handoff・memory 等の
  内部メタは書かない(`feedback_pr_no_estimates` (memory))。
- 実機 acc test の接続: Mac は homelab LAN 内、Hyper-V ホスト `10.0.0.100:5986`。資格情報は
  `~/repos/private/homelab-infra/terraform/hyperv/.envrc` の `TF_VAR_hyperv_password` を source。
  env: `HYPERV_USER=terraform HYPERV_PORT=5986 HYPERV_HTTPS=true HYPERV_INSECURE=true HYPERV_USE_NTLM=true`。
