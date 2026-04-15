# Milestones

このページは、`webp-guard` の開発テーマを大づかみに共有するためのものです。日付コミットメントではなく、優先度の見取り図として扱います。

## Phase 1: Core Conversion Safety

現在の基盤です。

- `jpg` / `jpeg` / `png` の bulk scan と変換
- magic bytes 検査
- file size / dimensions / pixel count 上限
- decode failure の検出
- symlink / hidden directory の既定スキップ
- report / manifest / verify

## Phase 2: Delivery Reliability

運用フェーズの整合性を厚くする領域です。

- cache-first を前提にした release / deploy plan
- artifact-relative manifest
- local publish
- verify-delivery
- 生成物と配信先の hash / metadata 検証

## Phase 3: Team Adoption

個人ツールからチーム運用へ寄せる改善です。

- 導入手順の整理
- config-first な運用導線
- CI/CD 向けのサンプル拡充
- contributor 向けドキュメントの整備

## Backlog Candidates

必要性がはっきりしたら検討する候補です。

- Linux arm64 の検証拡充
- より厚い release automation
- delivery provider の追加
- 大規模 asset tree 向けの運用補助

## Out of Scope For Now

少なくとも直近の主眼ではありません。

- 汎用画像解析ツール化
- 画像内容の意味解析
- マルウェア検査製品の代替
- 何でも変換できる総合メディアパイプライン化
