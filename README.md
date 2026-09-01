# Terraform Provider for Hyper-V

[taliesins/terraform-provider-hyperv](https://github.com/taliesins/terraform-provider-hyperv) のフォークです。
WinRM 経由で Hyper-V の VM・ネットワーク・ストレージを Terraform で管理します。

## フォーク独自の追加機能

### 新規リソース

| リソース | 概要 |
|----------|------|
| `hyperv_cloudinit_iso` | Cloud-Init NoCloud 互換 ISO をホスト上で直接生成 |
| `hyperv_vm_checkpoint` | VM チェックポイントの作成・復元・削除（`restore_on_destroy` 対応） |

### 既存リソースの拡張

| リソース | 追加属性 | 概要 |
|----------|----------|------|
| `hyperv_machine_instance` | `automatic_checkpoints_enabled` | 自動チェックポイントの有効/無効 |
| `hyperv_machine_instance` | `gpu_adapters` ブロック | GPU-P (GPU Partitioning) サポート |

### バグ修正・改善

- Gen 2 VM: IDE コントローラ使用時の plan 段階バリデーション追加
- Gen 2 VM: DVD/HDD BootOrder 判定を .NET 型チェックに変更（日本語ロケール対応）
- VM Notes: Base64 エンコードで Unicode 文字列を安全に WinRM 転送
- HardDisk/DVD ドライブの Update/Delete をインデックスベースに統一
- MAC アドレス `00-00-00-00-00-00` 生成防止（`dynamic_mac_address=true` 時）
- Go 1.26 対応、golang.org/x/net HTTP/2 脆弱性修正

### CI/CD

- GitHub Actions を最新バージョンに更新
- govulncheck によるセキュリティスキャン追加
- goreleaser v2 対応

## PowerShell 非依存化(移行中)

このフォークは、PowerShell 経由での Hyper-V 操作を
[go-wsman](https://github.com/r4sd/go-wsman)(Go ネイティブの WS-Man/CIM クライアント)へ
段階的に置き換えている。**移行中のため既定は従来どおり PowerShell 経路**で、
切り替えは環境変数で行う。

| 環境変数 | 有効時の挙動 | 既定 |
|----------|--------------|------|
| `HYPERV_USE_WSMAN=1` | CIM 経路に切り替える。未移行の操作は PowerShell へ自動的に落ちる | 無効 |
| `HYPERV_WSMAN_STRICT=1` | PowerShell へのフォールバックをエラーにする(PS 不使用の検証用) | 無効 |

`HYPERV_WSMAN_STRICT` は `HYPERV_USE_WSMAN` が有効なときだけ意味を持つ。

### 現在の移行状況

| リソース | CIM 経路 |
|----------|----------|
| `hyperv_vhd` | ✅ 対応(`source` / `source_vm` 指定時は PowerShell) |
| `hyperv_machine_instance` | ⚠️ 一部(下記) |
| `hyperv_network_switch` | ❌ PowerShell のみ |
| `hyperv_iso_image` | ❌ PowerShell のみ |
| `hyperv_cloudinit_iso` | ❌ PowerShell のみ |
| `hyperv_vm_checkpoint` | ❌ PowerShell のみ |

### CIM 経路で扱えない入力の 2 通りの挙動

**挙動が 2 種類あり、利用者から見た結果が大きく違う。**

#### A. 自動で PowerShell に落ちる(何もしなくても動く)

| 条件 | 備考 |
|------|------|
| **OS インストール済み Gen2 VM の firmware read** | **最も頻繁に踏む**。Windows Boot Manager の boot entry を CIM 側が解釈できないため。plan / refresh のたびに発生する |
| `wait_for_ips = true` | |
| 非ゼロ → 0 / false へのダウングレード | CIM はゼロ値を送れず黙殺されるため、意図的に委譲する |
| `secure_boot_template` が未知の GUID / 名前 | 既知の対応表に無いもの |
| `gpu_adapters` が非空 | 割り当ての CIM 実装が未着手 |
| `hyperv_vhd` の `source` / `source_vm` 指定 | ファイルコピー / VM ディスクキャプチャは CIM の範囲外 |

#### B. エラーで停止する(`HYPERV_USE_WSMAN` を外す必要がある)

**こちらは apply が失敗する。** 黙って PowerShell に落ちることはしない
(気付かないまま設定が反映されない事故を避けるため)。

| 条件 | 備考 |
|------|------|
| NIC の高度なオプション | QoS / IOV / MAC spoofing / 各種 guard / VLAN / 帯域 / チーミング / PacketDirect |
| ハードディスクの高度なオプション | QoS / パススルー / カスタムプール / キャッシュ属性 / 永続予約 |
| DVD の空メディア(ISO 未指定) | |
| チェックポイント種別の変更 | `checkpoint_type` / `automatic_checkpoints_enabled` の更新 |

**VLAN を使う構成などはここに該当する。** `HYPERV_USE_WSMAN=1` のままでは apply が通らない。

### PowerShell 0 件で通る条件

**`HYPERV_USE_WSMAN=1` を有効にしても PowerShell が不要になるわけではない。**

フルライフサイクルが PowerShell 0 件で通ることは実機で確認しているが、
成立する構成は狭く、次をすべて満たす場合に限られる。

- Gen2 VM で、**OS を入れていない**こと(入れると firmware read が PowerShell に落ちる)
- NIC がスイッチに接続されていないこと(go-wsman [#114](https://github.com/r4sd/go-wsman/issues/114) の既知バグ)
- `wait_for_ips = false`
- プロセッサとファームウェアが既定値のまま

つまり現時点では**使い捨ての検証用 VM でのみ成立する**。実運用の構成では PowerShell が動く。

移行の方式と判断の経緯は [`docs/adr/`](docs/adr/README.md) を参照。

## 動作環境

- [Terraform](https://www.terraform.io/downloads.html) >= 1.13.0
- [Go](https://golang.org/doc/install) 1.26（ビルドする場合）
- Hyper-V が有効な Windows ホスト + WinRM 設定済み

**検証済み環境**: Windows 11 Pro (Hyper-V)

upstream では Windows 10 / Windows Server 2016 以降に対応とされていますが、本フォークでは Windows 11 でのみ検証しています。

## インストール

本フォークは Terraform Registry に公開していないため、ローカルビルドで使用します。

```bash
# ビルド
go build -o dist/ .

# ~/.terraformrc に dev_overrides を設定
cat <<'EOF' > ~/.terraformrc
provider_installation {
  dev_overrides {
    "taliesins/hyperv" = "/path/to/terraform-provider-hyperv/dist"
  }
  direct {}
}
EOF
```

## Hyper-V ホストの WinRM 設定

### 基本設定

```powershell
# Hyper-V 有効化
Enable-WindowsOptionalFeature -Online -FeatureName:Microsoft-Hyper-V -All

# WinRM 有効化
Enable-PSRemoting -SkipNetworkProfileCheck -Force

Set-WSManInstance WinRM/Config/WinRS -ValueSet @{MaxMemoryPerShellMB = 1024}
Set-WSManInstance WinRM/Config -ValueSet @{MaxTimeoutms = 1800000}
Set-WSManInstance WinRM/Config/Client -ValueSet @{TrustedHosts = "*"}
Set-WSManInstance WinRM/Config/Service/Auth -ValueSet @{Negotiate = $true}
```

### HTTPS リスナー設定

自己署名証明書を使用する場合:

```powershell
# ホスト証明書作成
$hostName = [System.Net.Dns]::GetHostName()
$dnsNames = @($hostName, "localhost", "127.0.0.1") + [System.Net.Dns]::GetHostByName($env:computerName).AddressList.IpAddressToString

$cert = New-SelfSignedCertificate -DnsName $dnsNames -CertStoreLocation Cert:\LocalMachine\My

# HTTPS リスナー作成
Get-ChildItem wsman:\localhost\Listener\ | Where-Object -Property Keys -eq 'Transport=HTTPS' | Remove-Item -Recurse
New-Item -Path WSMan:\localhost\Listener -Transport HTTPS -Address * -CertificateThumbPrint $cert.Thumbprint -Force

Restart-Service WinRM

# ファイアウォールルール
New-NetFirewallRule -DisplayName "WinRM HTTPS" -Name "WinRMHTTPSIn" -Profile Any -LocalPort 5986 -Protocol TCP
```

### 接続テスト

```powershell
$cred = Get-Credential
$soptions = New-PSSessionOption -SkipCACheck -SkipCNCheck
Enter-PSSession -ComputerName $hostName -Port 5986 -Credential $cred -SessionOption $soptions -UseSSL
```

## Provider 設定例

```hcl
variable "hyperv_user" {
  type = string
}

variable "hyperv_password" {
  type      = string
  sensitive = true
}

variable "hyperv_host" {
  type = string
}

provider "hyperv" {
  user     = var.hyperv_user
  password = var.hyperv_password
  host     = var.hyperv_host
  port     = 5986
  https    = true
  insecure = true
  use_ntlm = true
  timeout  = "300s"
}
```

## 使用例

### Cloud-Init ISO + Ubuntu VM

```hcl
resource "hyperv_cloudinit_iso" "ubuntu" {
  destination_iso_file_path = "D:\\Hyper-V\\cloud-init\\ubuntu-01.iso"

  user_data = <<-EOF
    #cloud-config
    hostname: ubuntu-01
    users:
      - name: admin
        sudo: ALL=(ALL) NOPASSWD:ALL
        ssh_authorized_keys:
          - ${var.ssh_public_key}
  EOF

  meta_data = <<-EOF
    instance-id: ubuntu-01
    local-hostname: ubuntu-01
  EOF
}

resource "hyperv_machine_instance" "ubuntu" {
  name                          = "ubuntu-01"
  generation                    = 2
  processor_count               = 2
  static_memory                 = true
  memory_startup_bytes          = 4294967296  # 4GB
  automatic_checkpoints_enabled = false
  checkpoint_type               = "Disabled"

  # ...
}
```

### VM Checkpoint（Chaos Engineering 向け）

```hcl
resource "hyperv_vm_checkpoint" "pre_chaos" {
  vm_name            = "worker-01"
  checkpoint_name    = "pre-chaos-experiment"
  restore_on_destroy = true
}
```

## デバッグ

```bash
# Terraform ログ
export TF_LOG=DEBUG

# WinRM デバッグ
export WINRMCP_DEBUG=1
```

## 関連リンク

- [Upstream](https://github.com/taliesins/terraform-provider-hyperv)
- [Terraform Registry (upstream)](https://registry.terraform.io/providers/taliesins/hyperv/latest/docs)
