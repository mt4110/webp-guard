# WebP Guard

[English](./README_EN.md)

日本語を正本にしています。英語版は [README_EN.md](./README_EN.md) に分けています。

`webp-guard` は、安全に再実行できる bulk scan + WebP 生成と、cache-first 配信 planning を担う beta の Go CLI です。
元画像は残し、既定では隣に `.webp` を生成し、CI などで作業ツリーを汚したくないときは `-out-dir` 配下にミラー出力できます。変換後サイズが悪化した場合はその生成物を破棄します。

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
- local path を含まない public `release-manifest.json` を生成
- 環境別 `deploy-plan.json` を生成
- `conversion-manifest.json` と `deploy-plan.json` は machine 固有の absolute path ではなく artifact-relative に保持
- `publish -dry-run=plan`、local filesystem publish、`verify-delivery` に対応

## コマンド例

```bash
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

- Ubuntu / Linux: `scripts/bulk-webp.sh`
  - 第1引数に directory があればそれを優先
  - なければ `zenity` を試す
  - それも無ければ端末入力に fallback
- Windows: `scripts/bulk-webp.ps1`
  - 第1引数に directory があればそれを優先
  - なければ folder picker を試す
  - 最後は `Read-Host` に fallback

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
