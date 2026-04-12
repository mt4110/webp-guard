# Cache-First Delivery Design

## 目的

この設計は、画像変換と配信を次の前提で実装するためのものです。

- キャッシュヒット率を最優先にする
- ファイル内容の hash を URL に埋め込む
- purge 依存を最小化する
- 古い JPG / WebP が CDN やブラウザに残っても事故らない
- mutable なのは `HTML` と `release-manifest.json` のみ
- immutable なのは画像本体

この設計での真実は「今どの URL を参照しているか」です。古い画像ファイルが edge や browser cache に残ること自体は障害ではありません。

## Beta Positioning

CI/CD 配信機能は最初から「全 CDN / 全 object storage / 全オンプレサーバー対応」を約束しません。

beta で守るべきことは次です。

- 変換の正しさ
- hash URL による immutable 配信
- manifest / deploy plan の一貫性
- 少数の adapter で確実に動くこと
- dry-run と verify で事故を起こしにくいこと

beta で約束しないことは次です。

- あらゆる CDN ベンダーへの即時対応
- あらゆる object storage 実装差異の吸収
- すべての self-hosted runner 環境での再現性
- purge API の完全な互換吸収

公開時の表現は、少なくとも当面は `beta` または `experimental` が妥当です。`v1.0.0` を名乗るなら、対応範囲と非対応範囲を明文化した support matrix が必要です。

## Beta Scope

beta の対応範囲は明示的に絞ります。

### v0 Beta で対応するもの

- local filesystem での変換
- `conversion-manifest.json` / `release-manifest.json` / `deploy-plan.json`
- `plan`
- `publish -dry-run`
- `verify-delivery`
- 1つ以上の実用的 origin adapter
- 1つ以上の実用的 CDN adapter

### v0 Beta の推奨サポート対象

- Origin:
  - Local filesystem
  - S3
  - S3-compatible endpoint を 1 系統
- CDN:
  - Noop
  - CloudFront または Cloudflare のどちらか 1 つ

### beta で明確に非対応または未保証と書くもの

- Akamai / Google Cloud CDN / Azure Front Door / Fastly の一斉対応
- IDCF / さくら / GMO の各ストレージ実装差異
- Nginx / Apache / 独自 origin server への upload 自動化
- multi-CDN 同時 publish
- self-hosted runner 上での完全再現

## Quality Bar

「普通に利用できるレベル」の品質バーを先に定義します。

### beta 公開の最低条件

- 1000 枚規模のバルク変換で落ちない
- 破損画像 / 拡張子偽装 / 巨大画像に対して安全に失敗できる
- `plan` と `publish -dry-run` が人間に読める
- immutable upload と mutable upload の順序保証がある
- `verify-delivery` で最低限の header / status 検証ができる
- PR CI では secret なしで安全に検証できる
- DEV または STG で end-to-end が 1 系統通る

### v1.0.0 を名乗る前の条件

- support matrix が README にある
- 互換性ポリシーがある
- rollback 手順がある
- 少なくとも 2 系統の origin / CDN adapter 実績がある
- 主要 workflow が SHA pin 済み
- public manifest に内部情報を漏らさない

## 用語

- `conversion manifest`: ローカル変換と verify のための manifest
- `release manifest`: 配信用の正本 manifest
- `deploy plan`: 環境ごとの upload / purge / verify 指示書
- `logical path`: アプリから見た論理的な画像パス。例: `images/hero.jpg`
- `variant`: 実際に配信する派生ファイル。例: `images/hero.4f2c9a.webp`

## アーティファクト設計

### 1. conversion-manifest.json

既存の manifest を verify 用として継続利用します。用途は次です。

- 元画像と出力画像のローカル整合性確認
- encoder 設定の追跡
- report と verify の入力

この manifest は CDN や frontend runtime の正本にはしません。

### 2. release-manifest.json

配信の正本です。frontend 側、deploy 側、delivery verify 側の共通入力にします。

主な役割は次です。

- 論理画像名から現在の配信 URL を引く
- fallback と preferred format を明示する
- immutable asset の object key と hash を固定する
- rollback を「前の manifest に戻す」だけで成立させる

### 3. deploy-plan.json

環境別の publish 指示書です。次の情報を持ちます。

- どの asset をどこに upload するか
- どの header を付与するか
- どの mutable asset を後段で切り替えるか
- purge 対象
- CDN 越し verify 対象

`release-manifest.json` は環境非依存、`deploy-plan.json` は環境依存と切り分けます。

## ディレクトリと出力例

```text
out/
  conversion-manifest.json
  release-manifest.json
  deploy-plan.dev.json
  deploy-plan.stg.json
  deploy-plan.prod.json
  assets/
    images/
      hero.8f1c2e.jpg
      hero.4f2c9a.webp
```

## Hash と URL のルール

- hash は元画像ではなく「最終成果物 bytes」の `sha256` を使う
- URL に使う hash は先頭 10-16 桁程度に短縮してよい
- object key は `assets/<dir>/<name>.<hash>.<ext>` に統一する
- 同じ `logical_path` でも bytes が変わったら必ず新しい object key にする
- 古い object key はすぐ削除しない

推奨例:

```text
logical_path: images/hero.jpg
jpg object_key:  assets/images/hero.8f1c2ef0b3.jpg
webp object_key: assets/images/hero.4f2c9a77de.webp
```

## Go Struct: release-manifest.json

最初は `package main` 配下で定義して十分です。

```go
package main

type ReleaseManifest struct {
	SchemaVersion int                    `json:"schema_version"`
	GeneratedAt   string                 `json:"generated_at"`
	Build         ReleaseBuildInfo       `json:"build"`
	Assets        []ReleaseAsset         `json:"assets"`
	Mutable       ReleaseMutableMetadata `json:"mutable"`
}

type ReleaseBuildInfo struct {
	GitSHA      string              `json:"git_sha"`
	GitRef      string              `json:"git_ref,omitempty"`
	BuildID     string              `json:"build_id,omitempty"`
	Encoder     ReleaseEncoderInfo  `json:"encoder"`
	Quality     int                 `json:"quality"`
	MaxWidth    int                 `json:"max_width"`
	GeneratedBy string              `json:"generated_by,omitempty"`
}

type ReleaseEncoderInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type ReleaseMutableMetadata struct {
	ManifestKey   string `json:"manifest_key"`
	CacheControl  string `json:"cache_control"`
	ContentType   string `json:"content_type"`
	IntegrityHash string `json:"integrity_hash,omitempty"`
}

type ReleaseAsset struct {
	LogicalID       string           `json:"logical_id"`
	LogicalPath     string           `json:"logical_path"`
	Source          ReleaseSource    `json:"source"`
	Variants        []ReleaseVariant `json:"variants"`
	FallbackFormat  string           `json:"fallback_format"`
	PreferredFormat string           `json:"preferred_format"`
	Tags            []string         `json:"tags,omitempty"`
}

type ReleaseSource struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type ReleaseVariant struct {
	Format         string            `json:"format"`
	ContentHash    string            `json:"content_hash"`
	ObjectKey      string            `json:"object_key"`
	PublicPath     string            `json:"public_path,omitempty"`
	Bytes          int64             `json:"bytes"`
	Width          int               `json:"width"`
	Height         int               `json:"height"`
	ContentType    string            `json:"content_type"`
	CacheControl   string            `json:"cache_control"`
	ETag           string            `json:"etag,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	LocalPath      string            `json:"local_path,omitempty"`
}
```

### release-manifest の設計ポイント

- `LogicalID` は将来 alias や複数入力元が増えても安定する値にする
- `PublicPath` は CDN host を含めず path のみに留めると移植しやすい
- `LocalPath` は deploy plan 生成時に使えるので残してよい
- `Tags` は purge by tag 対応 CDN に備える
- `GeneratedBy` は build system 名程度に留め、外部に見せたくない実装主体名は入れない

## Go Struct: deploy-plan.json

`deploy-plan.json` は環境ごとの差分を持たせます。

```go
package main

type DeployPlan struct {
	SchemaVersion int                 `json:"schema_version"`
	GeneratedAt   string              `json:"generated_at"`
	Environment   string              `json:"environment"`
	Release       DeployPlanRelease   `json:"release"`
	Origin        OriginTarget        `json:"origin"`
	CDN           CDNTarget           `json:"cdn"`
	Uploads       []UploadRequest     `json:"uploads"`
	MutableUploads []UploadRequest    `json:"mutable_uploads"`
	Purge         PurgeRequest        `json:"purge"`
	Verify        DeliveryVerifyPlan  `json:"verify"`
}

type DeployPlanRelease struct {
	GitSHA              string `json:"git_sha"`
	ReleaseManifestPath string `json:"release_manifest_path"`
	ConversionManifestPath string `json:"conversion_manifest_path,omitempty"`
}

type OriginTarget struct {
	Provider   string            `json:"provider"`
	Bucket     string            `json:"bucket,omitempty"`
	Container  string            `json:"container,omitempty"`
	Endpoint   string            `json:"endpoint,omitempty"`
	Prefix     string            `json:"prefix,omitempty"`
	Region     string            `json:"region,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
}

type CDNTarget struct {
	Provider      string            `json:"provider"`
	ZoneID        string            `json:"zone_id,omitempty"`
	DistributionID string           `json:"distribution_id,omitempty"`
	ServiceID     string            `json:"service_id,omitempty"`
	BaseURL       string            `json:"base_url"`
	Capabilities  []string          `json:"capabilities,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
}

type UploadRequest struct {
	LocalPath      string            `json:"local_path"`
	ObjectKey      string            `json:"object_key"`
	ContentType    string            `json:"content_type"`
	CacheControl   string            `json:"cache_control"`
	ContentSHA256  string            `json:"content_sha256"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	Immutable      bool              `json:"immutable"`
	SkipIfSameHash bool              `json:"skip_if_same_hash"`
}

type PurgeRequest struct {
	Enabled bool     `json:"enabled"`
	URLs    []string `json:"urls,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	Keys    []string `json:"keys,omitempty"`
}

type DeliveryVerifyPlan struct {
	Enabled bool          `json:"enabled"`
	Checks  []VerifyCheck `json:"checks"`
}

type VerifyCheck struct {
	URL                string            `json:"url"`
	ExpectStatus       int               `json:"expect_status"`
	ExpectContentType  string            `json:"expect_content_type,omitempty"`
	ExpectCacheControl string            `json:"expect_cache_control,omitempty"`
	ExpectETag         string            `json:"expect_etag,omitempty"`
	ExpectHeaders      map[string]string `json:"expect_headers,omitempty"`
}
```

### deploy-plan の設計ポイント

- `Uploads` は immutable asset 専用
- `MutableUploads` は `release-manifest.json` と `index.html` など後段切り替え用
- `SkipIfSameHash` により再 upload を減らす
- `Purge.Enabled=false` でも運用できる設計にする
- `Capabilities` で CDN ごとの差を分岐しやすくする
- `dry-run` は plan の内容ではなく publish の実行 mode として扱う

## 追加する CLI サブコマンド案

### `webp-guard plan`

役割:

- `conversion-manifest.json` から `release-manifest.json` を生成する
- 指定環境向け `deploy-plan.json` を生成する

想定フラグ:

```bash
webp-guard plan \
  -conversion-manifest ./out/conversion-manifest.json \
  -release-manifest ./out/release-manifest.json \
  -deploy-plan ./out/deploy-plan.stg.json \
  -env stg \
  -base-url https://stg.example.com \
  -origin-provider s3 \
  -origin-bucket example-stg-assets \
  -cdn-provider cloudfront \
  -cdn-distribution E123456 \
  -immutable-prefix assets \
  -mutable-prefix release
```

### `webp-guard publish`

役割:

- `deploy-plan.json` を読み込んで upload / purge / verify を行う

想定フラグ:

```bash
webp-guard publish \
  -plan ./out/deploy-plan.stg.json \
  -dry-run=plan
```

`-dry-run` は bool ではなく mode にします。

- `off`: 実行する
- `plan`: API を叩かず、予定だけ表示する
- `verify`: origin / CDN の read-only verify のみ実行する

### `webp-guard verify-delivery`

役割:

- CDN 経由の header / status / content-type を検証する

## dry-run 設計

`dry-run` を曖昧な bool にすると、本当に何をしないのかが伝わりにくくなります。mode に分けます。

### 推奨 mode

- `plan`: upload しない、purge しない、verify しない
- `verify`: upload しない、purge しない、read-only verify のみ
- `off`: すべて実行

### CI での使い分け

- PR CI: `plan`
- DEV: `off` または `verify`
- STG: `off`
- PROD: `off`

### dry-run で必ず出すもの

- immutable upload 件数
- mutable upload 件数
- 変更 asset 一覧
- purge 対象
- verify 対象 URL
- 想定 cache-control

## DEV / STG / PROD の環境設計

### DEV

用途:

- 開発者確認
- 差分検証
- header と manifest の動作確認

推奨:

- 本番とは別 bucket / container / prefix
- CDN は共有でも path prefix を分ける
- `release-manifest.json` の TTL を短くする
- purge は URL 単位のみ

例:

```text
DEV base URL: https://dev-cdn.example.com
DEV immutable prefix: assets/dev/
DEV mutable prefix: release/dev/
```

### STG

用途:

- 本番相当の cache policy 検証
- rollback 手順確認
- delivery verify

推奨:

- 本番同等の cache-control
- 本番同等の CDN rule
- deploy は environment protection 付き

### PROD

用途:

- 本番配信

推奨:

- immutable upload 完了前に mutable を出さない
- OIDC による短命 credential
- required reviewers
- selected branches / tags 制限

## GitHub Actions の理想構成

### Workflow 1: PR validate

ファイル例: `.github/workflows/pr-validate.yml`

役割:

- scan
- bulk
- verify
- release manifest と deploy plan の生成確認
- publish はしない

たたき台:

```yaml
name: PR Validate

on:
  pull_request:
    branches: [main]

permissions:
  contents: read

jobs:
  validate:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@v4
        with:
          persist-credentials: false

      - name: Install Nix
        uses: cachix/install-nix-action@v27

      - name: Build
        run: nix develop --command go build -o webp-guard .

      - name: Bulk
        run: |
          mkdir -p out
          nix develop --command ./webp-guard bulk \
            -dir . \
            -manifest ./out/conversion-manifest.json \
            -report ./out/conversion-report.jsonl

      - name: Verify
        run: |
          nix develop --command ./webp-guard verify \
            -manifest ./out/conversion-manifest.json \
            -report ./out/verify-report.jsonl

      - name: Plan DEV
        run: |
          nix develop --command ./webp-guard plan \
            -conversion-manifest ./out/conversion-manifest.json \
            -release-manifest ./out/release-manifest.json \
            -deploy-plan ./out/deploy-plan.dev.json \
            -env dev \
            -base-url https://dev-cdn.example.com

      - name: Publish Dry Run
        run: |
          nix develop --command ./webp-guard publish \
            -plan ./out/deploy-plan.dev.json \
            -dry-run=plan

      - name: Upload Artifacts
        uses: actions/upload-artifact@v4
        with:
          name: webp-guard-pr-artifacts
          path: out/
```

### Workflow 2: Build release

ファイル例: `.github/workflows/build-release.yml`

役割:

- immutable asset を生成
- manifest / plan を artifact 化
- deploy は reusable workflow に委譲

artifact 契約は先に固定します。

- `out/conversion-manifest.json`
- `out/release-manifest.json`
- `out/deploy-plan.dev.json`
- `out/deploy-plan.stg.json`
- `out/deploy-plan.prod.json`
- `out/assets/**`
- optional: `out/reports/**`

### Workflow 3: deploy.yml

この workflow は `workflow_call` で再利用します。

ファイル例: `.github/workflows/deploy.yml`

```yaml
name: Deploy Assets

on:
  workflow_call:
    inputs:
      environment:
        required: true
        type: string
      deploy_plan:
        required: true
        type: string
      dry_run_mode:
        required: false
        type: string
        default: off

permissions:
  contents: read
  id-token: write

jobs:
  deploy:
    name: Deploy to ${{ inputs.environment }}
    runs-on: ubuntu-latest
    environment: ${{ inputs.environment }}
    concurrency:
      group: deploy-${{ inputs.environment }}
      cancel-in-progress: false
    steps:
      - name: Checkout
        uses: actions/checkout@v4
        with:
          persist-credentials: false

      - name: Install Nix
        uses: cachix/install-nix-action@v27

      - name: Build CLI
        run: nix develop --command go build -o webp-guard .

      - name: Download build artifacts
        uses: actions/download-artifact@v4
        with:
          name: webp-guard-build
          path: out/

      - name: Authenticate to cloud
        if: ${{ inputs.dry_run_mode == 'off' }}
        run: |
          echo "OIDC login step goes here"
          echo "Use provider-specific official login action or STS exchange"

      - name: Publish
        env:
          DEPLOY_PLAN: ${{ inputs.deploy_plan }}
          DRY_RUN_MODE: ${{ inputs.dry_run_mode }}
        run: |
          nix develop --command ./webp-guard publish \
            -plan "$DEPLOY_PLAN" \
            -dry-run="$DRY_RUN_MODE"

      - name: Verify delivery
        if: ${{ inputs.dry_run_mode != 'plan' }}
        env:
          DEPLOY_PLAN: ${{ inputs.deploy_plan }}
        run: |
          nix develop --command ./webp-guard verify-delivery \
            -plan "$DEPLOY_PLAN"
```

### 呼び出し側 workflow 例

```yaml
name: Deploy STG

on:
  push:
    branches: [main]

jobs:
  deploy-stg:
    uses: ./.github/workflows/deploy.yml
    with:
      environment: stg
      deploy_plan: ./out/deploy-plan.stg.json
      dry_run_mode: off
```

## publish の実行順序

publish は次の順で固定します。

1. `deploy-plan.json` を読み込み、validation
2. immutable uploads
3. `release-manifest.json`
4. mutable uploads
5. targeted purge
6. delivery verify

この順を崩すと、`HTML` や manifest が先に新 URL を指して 404 を起こす危険があります。

## Origin / CDN adapter の責務

### OriginAdapter

責務:

- object の存在確認
- content hash による skip 判定
- header 付き upload
- optional delete

```go
type OriginAdapter interface {
	Stat(ctx context.Context, key string) (ObjectMeta, error)
	Upload(ctx context.Context, req UploadRequest) error
	Delete(ctx context.Context, key string) error
}

type ObjectMeta struct {
	Key          string
	Size         int64
	ETag         string
	ContentType  string
	CacheControl string
	Metadata     map[string]string
}
```

### CDNAdapter

責務:

- purge by URL
- purge by tag
- delivery verify 補助

```go
type CDNAdapter interface {
	PurgeURLs(ctx context.Context, urls []string) error
	PurgeTags(ctx context.Context, tags []string) error
}
```

### 実装順序

1. `noop CDNAdapter`
2. `CloudFront URL purge`
3. `Cloudflare URL / tag purge`
4. `Akamai URL / tag purge`

最初から全部を同時に実装しないことを推奨します。

## セキュリティ設計

### CI/CD で危ないポイント

#### 1. `pull_request_target` で untrusted code を実行する

危険:

- fork 由来 PR のコードが secret や write token に触れる
- cache poisoning や artifact 汚染の起点になる

方針:

- PR 検証は `pull_request` を使う
- `pull_request_target` は deploy には使わない

#### 2. 長期 credential を secret に置く

危険:

- secret 漏えい時の影響が長い
- rotation が重い

方針:

- cloud access は OIDC で短命 token 化
- environment ごとに role / principal を分ける

#### 3. `GITHUB_TOKEN` の権限が広い

危険:

- 不要な write 権限があると repo 改ざん面が広がる

方針:

- job ごとに最小権限
- validate job は `contents: read`
- deploy job は `contents: read`, `id-token: write`

#### 4. third-party action を tag pin だけで使う

危険:

- tag の差し替えに追従してしまう

方針:

- 本番 deploy workflow は commit SHA pin を基本にする

#### 5. self-hosted runner に untrusted workflow を流す

危険:

- runner が持続的に汚染され得る
- 同居 job から secret が漏れる

方針:

- public repo や fork 起点ジョブでは GitHub-hosted runner 優先
- self-hosted を使うなら runner group 分離と単用途化
- `act` や自作 runner は補助検証として使い、正式な release gate にはしない

#### 6. shell injection

危険:

- PR title, branch name, manifest value をそのまま shell に埋めると壊れる

方針:

- env 経由で渡す
- shell 変数は quote する

#### 7. object key 汚染

危険:

- `../` や絶対 path 混入で意図しない場所に upload される

方針:

- `logical_path` と `object_key` を validation
- `..` 禁止
- 許可 prefix 制限

### GitHub 側の推奨設定

- `.github/workflows/` を `CODEOWNERS` 対象にする
- deploy workflow は GitHub Environment を使う
- `stg`, `prod` に required reviewers を付ける
- deploy branch / tag を制限する
- actions は Dependabot で監視する

## delivery verify の要件

最低限、次を確認します。

- `200 OK`
- `Content-Type`
- `Cache-Control`
- `content hash` 系 metadata または manifest との一致
- optional: `Age`, `CF-Cache-Status`, `X-Cache` など provider header

推奨 verify 対象:

- `release-manifest.json`
- 新しく差し替わった上位 N 件の画像
- `index.html`

## テスト戦略

### Unit Test

- release manifest 生成
- deploy plan 生成
- object key 正規化
- dry-run 判定
- upload 順序

### Integration Test

- `noop OriginAdapter`
- `memory OriginAdapter`
- `noop CDNAdapter`
- `verify-delivery` の header 判定

### Fixture Test

- 10 枚: 最小の手元確認
- 100 枚: CI の通常回帰
- 1000 枚: beta 品質ゲート
- 壊れたファイル、巨大画像、拡張子偽装ファイルを混ぜる

### Runner Strategy

- 正式 gate は GitHub-hosted runner を正本にする
- `act` は開発中の高速 smoke test 用と割り切る
- 自作 self-runner は provider credential を使う DEV/STG 検証用に限定する
- release 判定を `act` のみで通さない

### CI Test Matrix

- PR: `scan + bulk + verify + plan + publish dry-run`
- DEV: 実 deploy 可能
- STG: 実 deploy + verify
- PROD: tag or protected branch のみ

## 段階的な実装順

### Phase 1

- `release-manifest.json` の struct と writer
- `deploy-plan.json` の struct と writer
- `webp-guard plan`

### Phase 2

- `OriginAdapter`
- local or memory adapter で test
- `webp-guard publish -dry-run`

### Phase 3

- S3 系 origin adapter
- `verify-delivery`
- DEV deploy workflow

### Phase 4

- STG deploy
- CDN purge adapter
- rollback manifest 運用

### Phase 5

- PROD deploy
- old immutable asset の garbage collection

## 参考

- GitHub Actions secure use reference:
  https://docs.github.com/en/actions/reference/security/secure-use
- GitHub Actions OIDC:
  https://docs.github.com/en/actions/reference/security/oidc
- GitHub Actions deployments and environments:
  https://docs.github.com/en/actions/reference/workflows-and-actions/deployments-and-environments
- GitHub Actions events:
  https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows
- actions/checkout:
  https://github.com/actions/checkout
