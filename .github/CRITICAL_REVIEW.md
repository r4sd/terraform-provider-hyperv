# 批判的レビューの観点(provider 側)

DoD ⑥ の敵対的レビューを起動するときは、毎回プロンプトを書き起こさずこのファイルを指す。

> このリポジトリの `.github/CRITICAL_REVIEW.md` に従って
> `git diff <base>...<head>` を批判的にレビューして。
> 追加で見てほしい点: <この PR 固有の懸念>

レビュー側は **反証する立場**で読む。「問題なし」と言い切ってよい。忖度不要。
指摘には **CONFIRMED** / **PLAUSIBLE** を明示し、重大な順に並べる。

**共通の 6 観点は go-wsman の
[`.github/CRITICAL_REVIEW.md`](https://github.com/r4sd/go-wsman/blob/main/.github/CRITICAL_REVIEW.md)
を参照する**(Issue の発生経路 / 観測範囲を超えた主張 / 経路の混同 / テストの実効性 /
fixture の来歴 / 実環境情報)。ここには provider 固有のものだけを書く。

---

## provider 固有に見る点

### 1. PS 経路と CIM 経路のどちらの話か

**このリポジトリで最も事故が多い型。** 同じ resource でも `HYPERV_USE_WSMAN` の有無で
通る経路が変わり、挙動も違う。

- コメント・README・PR 本文の記述が、**どちらの経路について言っているか**明示されているか
- 片方の経路で確認した挙動を、もう片方にも当てはめて書いていないか
- shadow 実装を足したなら、**PS 経路側の挙動を変えていないか**

> 実例: PR #159 で同一 PR 内に 2 回。`static_mac_address` の回避策が CIM 経路では
> 効かないのに一般則として書き、逆に「無視される」を PS 経路にも適用して書いた。

### 2. Terraform SDK の意味論を実装の想像で語っていないか

`d.SetId` / tainted / refresh / Delete の順序など、SDK と Terraform core の挙動は
**想像ではなく GOMODCACHE のソースを読んで確認する。**

- tainted を作るのは SDK ではなく **Terraform core**
  (`node_resource_apply_instance.go` の `maybeTainted`)
- エラー時も `Resource.Apply` は `data.State()` を返すが、`State()` は ID が空だと nil
- **既定では refresh が走る。** 「次の apply で Delete が呼ばれる」系の主張は、
  refresh-Read が state から外す経路を潰してからでないと成立しない

> 実例: PR #161 で「Delete が詰む」と書いたが、既定 refresh では起きず
> `-refresh=false` 限定だった。

### 3. 恒常 diff を作っていないか

Read が返す値と config が食い違うと、**apply のたびに VM が停止する**。
このリポジトリで唯一「実害」が出る型。

- 書き込んだ値が Read でそのまま返るか(read と write で wire format が違うことがある)
- 片方の経路でだけ正規化していないか
- `DiffSuppress` を足したなら、**未知の値まで抑止していないか**(既知の同士だけ抑止する)

### 4. shadow 実装の gap を黙って通していないか

CLAUDE.md の shadow 移行 DoD §4「**黙って成功報告する実装は禁止**」。
扱えない入力は検出して PS フォールバックか明示エラーにする。
「とりあえず無視する」は silent corruption になる。

### 5. 破壊的変更か

- 既存 state を持つユーザーの次の apply で何が起きるか
- schema の既定値を変えていないか
- 「今まで通っていた config」が落ちるようになるなら、**それが意図した改善か**を PR に書く
