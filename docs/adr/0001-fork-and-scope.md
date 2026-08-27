# ADR-0001: fork の理由とスコープ

> テーマ単位のファイル。決定が変わったら**末尾に追記**する(過去を書き換えない)。
> 詳しい運用は [README.md](README.md) を参照。

## 現在の決定

**taliesins/terraform-provider-hyperv の soft-fork として、PowerShell 依存を
[go-wsman](https://github.com/r4sd/go-wsman)(CIM ネイティブ)へ段階的に置き換える。**
上流へ還元することは目指さず、module path は taliesins のまま据え置く。

| 項目 | 内容 |
|---|---|
| 状態 | 進行中 |
| 最終更新 | 2026-08-27 |
| 関連 | `api/hyperv-winrm/`(PS 経路), `api/hyperv-wsman/`(CIM 経路), `CLAUDE.md` |

---

## 2026-05: soft-fork という形を選ぶ

### 背景

Terraform から Hyper-V を管理する既存の選択肢を評価した。

いずれも **Go プロセスから PowerShell を起動して Hyper-V を操作する**構造で、
そこから来る問題(バージョン差・エラーが文字列・型の喪失・失敗箇所の切り分け困難)を抱えていた。
詳しくは [go-wsman](https://github.com/r4sd/go-wsman) 側の ADR-0001「PowerShell に依存しない理由」に記録している。

### 選択肢と評価

| 案 | 内容 | 判断 |
|---|---|---|
| A: taliesins を soft-fork し中身を入れ替える | スキーマ互換を保ったまま実装だけ差し替える | ✅ 採用 |
| B: taliesins に PR を送る | 上流を CIM 化する | ❌ 却下: 事実上の全面書き換えでレビュー不能 |
| C: ゼロから新しい provider を書く | スキーマも作り直す | ❌ 却下: 既存の tf ファイルが使えなくなる |
| D: windsorcli/hyperv を使う | 活発だが PowerShell 埋め込み方式 | ❌ 却下: PS 依存が残る |

### 決定

A を採用。

- **module path は taliesins のまま**にし、ローカルでは Terraform の `dev_overrides` で参照する
- リソーススキーマ(`hyperv_machine_instance` 等の属性名)は変えない
- `api/` の下を PS 実装(`hyperv-winrm/`)と CIM 実装(`hyperv-wsman/`)の 2 系統に分け、
  後者へ 1 機能ずつ移す(方式は [ADR-0002](0002-shadow-migration.md))

### 根拠

**スキーマを変えない**ことを最優先にした。既存の `.tf` を書き換えずに実装だけ差し替えられれば、
移行中でも壊れた時点で feature flag を戻すだけで復旧できる。
C(作り直し)を選ぶとこの退路がなくなる。

**module path を変えないのは、公開して配布する予定がないため。**
レジストリに出すなら別 path が必要になるが、その場合は上流との関係を整理する必要がある。
現時点でその判断はしていない。

### スコープ(正直に書く)

**このプロジェクトは汎用の Hyper-V provider を目指していない。**

- 対象は **Windows 11 Hyper-V** のワークグループ構成
- Windows Server 固有機能(レプリカ、フェールオーバークラスタリング)は対象外
- 「PowerShell 依存を外す」ことが目的であり、機能網羅は目的ではない

**実運用で Hyper-V provider が必要なら [windsorcli/hyperv](https://github.com/windsorcli/terraform-provider-hyperv) の方が適している。**
活発に開発されており、PowerShell 依存を許容できるなら合理的な選択肢。
本プロジェクトが存在するのは前提(PS を外したい)が違うからにすぎない。

### 見直しの条件

- [ ] 配布したくなった → module path とライセンス表記、上流との関係を整理する
- [ ] Windows Server 環境で使いたくなった → スコープを広げるか、別の provider に移るかを判断する
- [ ] PowerShell 依存が完全に外れた → PS 経路の扱いを [ADR-0002](0002-shadow-migration.md) で再検討する
