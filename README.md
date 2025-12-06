# WebP Guard (WebP一括変換・安全装置)

[English](#english) | [日本語](#japanese)

---

<a name="english"></a>

# English

A blazing fast, safe, and CI/CD-friendly CLI tool to generate WebP images from PNGs.
Designed to be used in frontend repositories to ensure all assets have optimized WebP counterparts.

## Features

- **🚀 Deeply Recursive**: Scans all subdirectories for `.png` files.
- **🛡️ Safe-Guard**: 
    - **Dual Generation**: Keeps original `.png` and creates `.webp` next to it. No broken links.
    - **Size Check**: If the generated WebP is larger than the original PNG, it is automatically discarded.
- **Zero Dependency Hell**: Built with Go and Nix. No Node.js or Docker daemon required.

## Usage

### Local Development (with Nix)

```bash
# Enter the environment
nix develop

# Run the tool
go run main.go -dir ./src -quality 80
```

### Installation (Binary)

```bash
# Build
go build -o webp-guard .

# Run
./webp-guard -dir ./assets
```

## Options

- `-dir string`: Root directory to scan (default ".")
- `-quality int`: WebP quality 0-100 (default 75)
- `-overwrite`: Force overwrite existing .webp files (default false)
- `-dry-run`: Print what would happen without doing it (default false)

## CI/CD Integration

This tool is designed to run in GitHub Actions.
See `.github/workflows/optimize.yml` for example configuration.

## Why "Dual Generation"?

To avoid "broken image links" in production.
Instead of replacing `image.png` with `image.webp` (which requires updating all HTML/JS references), we generate `image.webp` *alongside* it.
You can then configure your CDN (CloudFront/Nginx) to serve the WebP version to supported browsers automatically.

---

<a name="japanese"></a>

# 日本語

PNG画像を高速かつ安全にWebPへ一括変換するCLIツールです。
フロントエンドリポジトリ内のアセット画像を、CI/CDで自動的に最適化することを想定して設計されています。

## 特徴

- **🚀 爆速・再帰スキャン**: 指定フォルダ以下を再帰的にスキャンし、すべての `.png` を対象にします。
- **🛡️ セーフガード (Safety First)**: 
    - **Dual Generation (並行生成)**: 元の `.png` は削除せず、隣に `.webp` を生成します。これにより、既存コードのリンク切れリスクをゼロにします。
    - **サイズチェック**: 万が一、WebP変換によってファイルサイズが増えてしまった場合（画質設定などにより稀に発生）、そのWebPファイルは自動的に破棄され、元画像が優先されます。
- **依存地獄からの解放**: GoとNixで作られているため、重い `node_modules` や Dockerデーモンは不要です。

## 使い方

### ローカルでの実行 (Nix推奨)

```bash
# 環境に入る (cwebpなどが使えるようになります)
nix develop

# ツールを実行
go run main.go -dir ./src -quality 80
```

### ビルドして使う

```bash
# ビルド
go build -o webp-cli .

# 実行
./webp-cli -dir ./assets
```

## オプション

- `-dir string`: スキャン対象のディレクトリ (デフォルト: ".")
- `-quality int`: 変換品質 0-100 (デフォルト: 75)
- `-overwrite`: 既に .webp があっても強制的に上書きするか (デフォルト: false)
- `-dry-run`: 実行せずに、何が変換されるかログだけ表示する (デフォルト: false)

## CI / CD インテグレーション

GitHub Actionsなどでの実行に最適化されています。
リポジトリ内の `.github/workflows/optimize.yml` を参照してください。

## なぜ「並行生成 (Dual Generation)」なのか？

本番環境での「リンク切れ画像 (404)」を防ぐためです。
`image.png` を `image.webp` に**置換**してしまうと、HTMLやJS内の `src="image.png"` という記述をすべて書き換える必要があり、多大なリスクを伴います。
本ツールは `image.webp` を**追加生成**するだけなので、コード修正は不要です。
CloudFrontやNginxの設定で、「ブラウザがWebP対応ならWebPを返す」設定をするだけで、安全にWebP配信が可能になります。

## License

MIT
