# WebP Guard

日本語 | [英語](./README_EN.md) | [セキュリティ](./SECURITY.md) | [コントリビュータ](./CONTRIBUTING.md)

日本語を正本にしています。英語版は [README_EN.md](./README_EN.md) に分けています。

`webp-guard` は、安全に再実行できる bulk scan + WebP 生成と、cache-first 配信 planning を担う beta の Go CLI です。
元画像は残し、既定では隣に `.webp` を生成し、CI などで作業ツリーを汚したくないときは `-out-dir` 配下にミラー出力できます。変換後サイズが悪化した場合はその生成物を破棄します。

関連設計:

- [Security Policy](./SECURITY.md)
- [Contributing](./CONTRIBUTING.md)
- [Milestones](./MILESTONES.md)
- [Release Workflow And Installation Design](./docs/release-installation-design.md)

## Installation

### Recommended: Release Binary

まず試すなら、[GitHub Releases](https://github.com/mt4110/webp-guard/releases/latest) から archive を取り、`webp-guard` を `PATH` に置く経路を正本にします。

最初の確認は `cwebp` なしで進められます。

```bash
webp-guard version
webp-guard help
webp-guard scan --dir ./assets
webp-guard bulk --dir ./assets --dry-run
```

実変換に進む前に `cwebp` を入れます。

- macOS: `brew install webp`
- Ubuntu / Debian: `sudo apt-get update && sudo apt-get install -y webp`
- Windows: [Google の WebP utilities](https://developers.google.com/speed/webp/download) を取得して、展開した `bin` を `PATH` に追加

### Go Install

Go toolchain を自前で管理しているならこちらです。

```bash
go install github.com/mt4110/webp-guard@latest
```

この場合も、実変換に進む前に `cwebp` を別途入れます。

### Nix

contributor 向けの再現性ある開発導線はそのまま残します。

```bash
nix develop
go test ./...
go build -o webp-guard .
```

### Support Matrix

| Target | Phase 1 validation |
| --- | --- |
| macOS arm64 | release build/package + native smoke |
| macOS amd64 | release build/package |
| Linux amd64 | release build/package + native smoke |
| Windows amd64 | release build/package + native smoke |
| Linux arm64 | backlog |

### `cwebp` Required?

| コマンド | `cwebp` |
| --- | --- |
| `scan` | 不要 |
| `verify` | 不要 |
| `plan` | 不要 |
| `publish` | 不要 |
| `verify-delivery` | 不要 |
| `bulk` | `-dry-run` 以外では必要 |
| `resume` | `-dry-run` 以外では必要 |

## Quick Start

### まず1回通す

最初は `cwebp` がなくても進められるところから始めます。

```bash
webp-guard version

webp-guard scan --dir ./assets --report ./out/scan.jsonl

webp-guard bulk \
  --dir ./assets \
  --out-dir ./out/assets \
  --dry-run \
  --report ./out/bulk-plan.jsonl
```

### config-first で短く使う

フラグを毎回書きたくないなら、最初に config を作るのが最短です。

```bash
webp-guard init
# webp-guard.toml を project に合わせて調整

webp-guard bulk --dry-run
webp-guard bulk
webp-guard verify
```

`bulk` や `verify` だけで回せる状態まで持っていくと、日常運用の手順がかなり短くなります。

## 現状 repo の理解

- もともとは Go 製の小さな CLI で、再帰的に PNG を見つけて `cwebp` を叩く構成でした。
- 既存の思想はすでに筋が良く、
  - 元画像を残す
  - 隣に `.webp` を作る
  - 生成物が大きければ捨てる
  という `safe-guard` の芯はそのまま活かせます。
- 足りなかったのは、大量件数を回すための本体設計でした。
  - jpg/jpeg/png 混在
  - width 上限と no-upscale
  - security scan
  - report / manifest
  - resume / verify
  - Windows / Ubuntu から迷わず叩ける薄い wrapper

## beta でできること

- 対応入力は `jpg`, `jpeg`, `png` のみ
- `.webp`, `gif`, `svg`, `heic`, `heif`, `avif` など他の画像入力は reject
- `width > 1200` のときだけ縮小
- `width <= 1200` はそのまま
- アスペクト比維持
- `-aspect-variants 16:9,4:3,1:1` で、16:9 を主力にしつつ 4:3 / 1:1 の補助 WebP を追加生成
- クロップは `-crop-mode safe` の中央寄せ安全クロップ、または `-crop-mode focus --focus-x ... --focus-y ...` の焦点指定に対応
- JPEG の EXIF orientation を反映してから変換
- metadata は既定で strip
- 元ファイルは削除しない
- 出力は元ファイルの隣、または `-out-dir` 配下に生成
- 拡張子と magic bytes の不一致を検出
- 壊れた画像や decode 不能画像を検出
- 異常に大きい file size / dimensions / pixel count を reject
- symlink は既定で follow しない
- hidden directory と代表的な system/VCS directory は既定で skip
- 全件一括読込ではなく、1ファイルずつ流して worker pool で並列化
- TTY では進捗バーと ETA を出し、長時間バッチでも今どこにいるか分かる
- SIGINT / SIGTERM を受けたら context cancel を流し、一時ファイルを掃除して止まる
- local path を含まない public `release-manifest.json` を生成
- 環境別 `deploy-plan.json` を生成
- `conversion-manifest.json` と `deploy-plan.json` は machine 固有の absolute path ではなく artifact-relative に保持
- `publish -dry-run=plan`、local filesystem publish、`verify-delivery` に対応
- `webp-guard.toml` を cwd から親方向へ自動探索し、project ごとの既定値を共有できる
- `init` で starter config を生成できる
- `doctor` で config discovery / `cwebp -version` / temp dir / 代表 path を診断できる
- `completion` で shell completion script を生成できる
- 人間向けログは stderr、`-json` の機械可読 summary は stdout に分離できる

## コマンド例

```bash
webp-guard version

webp-guard scan --dir ./assets --report ./reports/scan.jsonl

webp-guard bulk \
  --dir ./assets \
  --out-dir ./out/assets \
  --cpus 4 \
  --max-width 1200 \
  --quality 82 \
  --workers auto \
  --report ./out/conversion-report.jsonl \
  --manifest ./out/conversion-manifest.json

webp-guard bulk \
  --dir ./assets \
  --out-dir ./out/assets \
  --max-width 1200 \
  --aspect-variants 16:9,4:3,1:1 \
  --crop-mode safe \
  --report ./out/seo-report.jsonl \
  --manifest ./out/seo-manifest.json

webp-guard resume \
  --dir ./assets \
  --out-dir ./out/assets \
  --resume-from ./out/conversion-report.jsonl \
  --report ./out/conversion-resume.jsonl \
  --manifest ./out/conversion-manifest-resume.json

webp-guard verify \
  --dir ./assets \
  --manifest ./out/conversion-manifest.json \
  --report ./out/verify-report.jsonl

webp-guard plan \
  --conversion-manifest ./out/conversion-manifest.json \
  --release-manifest ./out/release-manifest.json \
  --deploy-plan ./out/deploy-plan.dev.json \
  --env dev \
  --origin-provider local \
  --origin-root ./out/dev-origin

webp-guard publish \
  --plan ./out/deploy-plan.dev.json \
  --dry-run plan

webp-guard publish \
  --plan ./out/deploy-plan.dev.json \
  --dry-run off

webp-guard verify-delivery \
  --plan ./out/deploy-plan.dev.json

webp-guard init

webp-guard doctor

webp-guard help publish

webp-guard completion zsh > ~/.zsh/completions/_webp-guard

webp-guard bulk \
  --json \
  --dry-run \
  --dir ./assets > ./out/bulk-summary.json
```

従来形式も互換で残しています。

```bash
webp-guard -dir ./assets -dry-run
```

## 主要フラグ

- `-dir`: スキャン対象ルート
- `-extensions`: 対象拡張子。`jpg,jpeg,png` の範囲だけ指定可能
- `-include`, `-exclude`: repeatable / comma-separated glob
- `-cpus`: 使用する論理 CPU 数。`auto` も可
- `-workers`: 並列数または `auto`
- `-cpus` は Go runtime の CPU 使用上限で、`-workers` の上限にも効く
- 例: `--cpus 4 --workers auto` のとき、worker 数は `4`
- `-quality`: WebP 品質 `0-100`。既定 `82`
- `-max-width`: 縮小上限。既定 `1200`
- `-aspect-variants`: 追加生成するアスペクト比。先頭が primary output になる
- `-crop-mode`: `safe` または `focus`
- `-focus-x`, `-focus-y`: `-crop-mode=focus` のときに使う 0.0-1.0 の焦点座標
- `-out-dir`: `.webp` を source tree を保ったまま別ディレクトリへ出す
- `-on-existing`: `skip`, `overwrite`, `fail`
- `-overwrite`: 互換用エイリアス。`-on-existing=overwrite`
- `-dry-run`: 実ファイルを書かずに計画だけ出す
- `-report`: `.jsonl` または `.csv`
- `-manifest`: `verify` と `plan` に渡す conversion manifest
- `verify -dir`: すでに artifact-relative な manifest に対する source root override
- `-resume-from`: 以前の report を読んで完了済みを skip するが、現在の変換設定と fingerprint が一致したものだけを再利用
- `-follow-symlinks`: root 内に収まる symlink だけ追従
- `-include-hidden`: dot file / dot directory も対象化
- `plan -release-manifest`: local absolute path を含まない public manifest
- `plan -deploy-plan`: 環境別 upload / purge / verify 指示書
- `publish -dry-run`: `off`, `plan`, `verify`
- `-config`: 明示的に `webp-guard.toml` を指定
- `-no-config`: config 自動読込を無効化
- `-json`: human log を stderr に残したまま summary JSON を stdout に出す

## Help / Doctor / Completion

- `webp-guard version` で埋め込み build metadata を確認
- `webp-guard help <command>` で subcommand ごとの使い方を確認
- `webp-guard -h`、`webp-guard --help`、`webp-guard <subcommand> -h` は usage を出して exit code `0`
- `webp-guard doctor` で config 自動発見、`cwebp -version`、temp dir の書き込み可否、CPU 認識、config 内の代表 input path を確認
- `webp-guard completion bash|zsh|fish|powershell` で補完 script を stdout に出力
- `doctor -json` は CI などから機械可読に扱える

## Config File

- 既定の設定ファイル名は `webp-guard.toml`
- 指定しなければ、現在の working directory から親ディレクトリ方向へ自動探索
- config 内の相対 path は、実行時の cwd ではなく config file 自身の場所を基準に解決
- 優先順位は `CLI flags > webp-guard.toml > built-in defaults`
- 毎回長い flag を書きたくないなら、`webp-guard init` で starter config を生成してから詰めるのが最短

## Security Scan 方針

`scan` と `bulk` は、変換前に同じ安全チェックを通します。

- extension と magic bytes の不一致を reject
- PNG/JPEG(JPG) 以外の画像入力を reject
- 未知の signature を reject
- 上限超過 file size を reject
- 上限超過 dimensions を reject
- 上限超過 pixel count を reject
- supported format は full decode して破損を検出
- symlink は既定で skip
- hidden/system/VCS directory は既定で skip
- 出力は元画像の隣、または宣言済み `-out-dir` 配下に限定し、危険な path 展開をしない

既定値:

- `max-file-size-mb=100`
- `max-pixels=80000000`
- `max-dimension=20000`
- `follow-symlinks=false`
- `include-hidden=false`

## Conversion / Release / Deploy Artifacts

- conversion report:
  - streaming に強い `.jsonl`
  - 表計算向け `.csv`
- conversion manifest:
  - 成功変換のみを積む内部向け JSON
  - source/output path は machine 固有の absolute path ではなく artifact-relative で保持
  - dimensions, quality, resize policy, size saving を保持
  - `verify` / `plan` の入力になる
- release manifest:
  - 配信用の public JSON
  - logical path と immutable object key / public path を結びつける
  - local filesystem path は含めない
- deploy plan:
  - 環境別の upload / purge / verify 指示書
  - upload 入力は plan artifact からの相対 path で保持する
  - immutable upload 用ファイルは final object key の配置で artifact 内に stage する
- path portability:
  - artifact-relative manifest/plan は同系統のパス解決環境で持ち運ぶ前提
  - Windows ネイティブの `C:\...` と WSL2 の `/mnt/c/...` は別環境として扱い、境界を跨ぐときは manifest / plan を再生成する
- resume:
  - 過去 report の terminal state を読んで再処理を避ける
  - ただし現在の実行設定と fingerprint が一致した entry だけを再利用する
- verify:
  - source/output の存在
  - source size の変化
  - output size の逆転
  - output dimensions
  を検証
- verify-delivery:
  - deploy plan に基づいて publish 後の URL または local file target を検証

## 終了コード

- `0`: 問題なく完了
- `1`: CLI/config/runtime の設定エラー
- `2`: scan/bulk は完走したが rejected/failed があった
- `3`: verify は完走したが不整合があった

## Ubuntu / Windows Wrapper

wrapper はあくまで入口だけです。
本体処理は必ず Go CLI が担当します。

日常運用は、Windows ネイティブよりも WSL2 + Ubuntu/Debian から `scripts/bulk-webp.sh` を使う経路を推奨します。

- Ubuntu / Linux: `scripts/bulk-webp.sh`
  - 第1引数に directory があればそれを優先
  - なければ `zenity` を試す
  - それも無ければ端末入力に fallback
- Windows: `scripts/bulk-webp.ps1`
  - 第1引数に directory があればそれを優先
  - なければ folder picker を試す
  - 最後は `Read-Host` に fallback
  - repo root 基準の path 解決には揃えているが、この branch では実機の動作確認はまだしていない

forward した相対 path は、`go run` / built binary / PATH binary のどれでも repo root 基準で解決されます。

例:

```bash
./scripts/bulk-webp.sh ./assets --report ./reports/bulk.jsonl --manifest ./reports/manifest.json
```

```powershell
./scripts/bulk-webp.ps1 C:\site\assets --report .\reports\bulk.jsonl --manifest .\reports\manifest.json
```

## まだ ffmpeg を採らなかった理由

`ffmpeg` 案は検討できますが、v1 は採用していません。

- 既存 repo がすでに `cwebp` ベースだった
- 静止画変換の最初の拡張としては依存面を増やしすぎない方が保守しやすい
- 今回の主戦場は encoder 差し替えではなく、scan / resume / manifest / verify の CLI 本体設計だった
- 将来、より広い format 対応に踏み込む段階では `ffmpeg` は再検討余地あり

## 開発

### Nix

```bash
nix develop
go test ./...
go build -o webp-guard .
```

### Go

Go と `cwebp` を入れた上で:

```bash
go test ./...
go build -o webp-guard .
```

## 大量件数向けの注意

- まずは `scan` または `bulk -dry-run` で状態を見る
- CI や release job では `-out-dir ./out/assets` を使うと source tree を汚しにくい
- `workers=auto` か控えめな整数から始める
- マシンを重くしすぎたくない場合は `-cpus 4` のように明示する
- 中断時は最初からやり直さず `resume` を使う
- 1万件級では report は `.jsonl` 推奨
- 可能なら report / manifest はスキャン対象外ディレクトリに置く

## DB 連携サンプル

DB に conversion 結果を反映したいケース向けに、[example_db_upsert_batch](./example_db_upsert_batch) を置いています。

- Go の標準 `database/sql` ベース
- batched multi-row `UPSERT` を worker pool で並列実行
- PostgreSQL / MySQL / SQLite の SQL 方言を切り替え
- 単一の「完全に汎用な UPSERT SQL」は無いので、実行骨格を共通化し、SQL だけを差し替える方針

## beta では未対応 / 次フェーズ

- GIF/SVG/HEIC/AVIF/WebP 入力対応
- metadata keep モード
- 追加の origin / CDN adapter
- HEIC/AVIF decoder 統合
- content-aware な encoder 切り替え

## License

MIT
