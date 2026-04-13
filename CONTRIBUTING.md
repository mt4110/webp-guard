# Contributing

`webp-guard` への contribution を歓迎します。大きな変更ほど、先に方針をそろえてから進めるほうがレビューも実装も滑らかです。

## 基本方針

- README の日本語版を正本として扱います。
- 既存の CLI 体験を崩さない、小さく追いやすい変更を歓迎します。
- セキュリティ関連の挙動は、便利さより fail-closed を優先します。
- ドキュメント、help text、report/manifest の整合を大切にします。

## 開発環境

```bash
go test ./...
go build -o webp-guard .
```

`nix develop` が使える環境では、再現性のある開発シェルも利用できます。

## 変更前に共有してほしいこと

次のような変更は、実装前に issue や discussion で方向を共有してください。

- CLI の flag や subcommand を増やす変更
- manifest / report schema を変える変更
- security policy や既定値に影響する変更
- delivery / publish フローに触る変更

## 期待する変更の形

- 変更理由が README やコードから読めること
- 新しい reject / verify / publish 振る舞いにはテストがあること
- 既定値を変える場合は、なぜその値か説明があること
- 破壊的変更は移行方法を併記すること

## Pull Request の目安

- 1 つの PR では 1 つの主題に絞る
- ユーザー影響がある場合は README / help / docs も更新する
- 実行した確認手順を本文に書く
- セキュリティや path handling に関わる変更は、想定する failure mode を説明する

## セキュリティに関する変更

次の領域は、特に慎重なレビュー対象です。

- format detection
- decode / encode 前後の validation
- symlink / path containment
- manifest / plan / publish の path 解決
- temp file cleanup と signal handling

便利に見える変更でも、fail-open になるなら採用しません。その判断は厳しめで問題ありません。

## コミュニケーション

荒っぽい正しさより、後から読み返せる説明のある変更を歓迎します。小さく、明確に、理由つきで進めてもらえると助かります。
