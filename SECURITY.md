# Security Policy

## Security Philosophy

`webp-guard` は、画像最適化そのものより先に、失敗の仕方を設計するためのローカルファーストな Go CLI です。想定外の入力を黙って通すのではなく、拡張子偽装、破損画像、過大入力、危険なパス、配信時のドリフトを検出した時点で処理を止め、理由を記録し、元画像を残したまま終了することを基本方針にしています。

このツールは、CI/CD やバッチ変換に組み込むための build/pipeline safety tool です。マルウェア検査、DLP、OCR ベースの秘密情報検出、画像内容の意味解析は目的に含みません。

## Threat Model

`webp-guard` が主に低減しようとしているリスクは、次の通りです。

- 拡張子だけが `jpg` や `png` に見える偽装ファイル
- 壊れた画像、途中で切れた画像、decode 時に失敗する画像
- 極端に大きい file size、dimensions、pixel count によるメモリ圧迫や OOM 狙いの入力
- symlink や相対パスを悪用した、意図しないディレクトリへの到達
- `.git` や system directory を誤って走査対象に含める運用事故
- 変換中断時に中途半端な一時ファイルや不完全な出力が残ること
- manifest や deploy artifact に machine 固有の絶対パスが混入すること
- publish/verify 時に、想定外の object key や差し替わった artifact を配信してしまうこと

一方で、`webp-guard` は「画像の中身が無害であること」までは証明しません。守る対象は、主に file/process integrity、resource control、path handling、artifact consistency です。

## Security Architecture And Validations

### 1. 入力フォーマットは allowlist で限定

- 変換対象は `jpg`、`jpeg`、`png` のみです。
- `webp`、`gif`、`svg`、`heic`、`heif`、`avif` などは入力として受け付けず、reject します。
- 未知のシグネチャは `unknown` として reject されます。

「画像っぽいファイルなら通す」挙動を避け、扱うフォーマットを最初から狭く固定しています。

### 2. 拡張子と magic bytes の不一致を fail-closed で拒否

- 拡張子から期待されるフォーマットと、実ファイルの magic bytes を突き合わせます。
- 例えば `broken.jpg` の実体が PNG なら、変換前に `rejected_magic_mismatch` として処理を止めます。

これにより、拡張子偽装や誤配備ファイルを、decode や encode に進める前に弾きます。

### 3. Decode 前に resource upper bound を検査

- file size は `os.Stat` で先に確認し、既定値では `100 MB` を超える入力を reject します。
- dimensions は `DecodeConfig` で確認し、既定値では width または height が `20,000 px` を超える入力を reject します。
- pixel count は `width * height` で算出し、既定値では `80,000,000` pixels を超える入力を reject します。

この 3 段階の上限により、画像版の zip bomb や極端な高解像度入力でワーカーのメモリ消費が暴れる前に止められます。しきい値は config や flag で環境に合わせて調整できます。

### 4. 変換前にフルデコードして、壊れた画像を早期に隔離

- `DecodeConfig` だけでなく、対応フォーマットは本デコードまで行います。
- decode に失敗した画像は `failed_decode` または `failed_decode_config` として記録され、変換処理に進みません。

これは「拡張子は正しいが、実体は壊れている」入力を早い段階で見つけるためです。panic を防ぐだけでなく、失敗を report に残せる形で扱います。

### 5. JPEG orientation を反映してから出力を生成

- JPEG は EXIF orientation を読み取り、必要なら回転・反転を適用してから後続処理に渡します。
- `verify` でも manifest 上の expected dimensions と実出力の dimensions を再確認します。

これは直接の脆弱性対策ではありませんが、「見た目は回っているのにサイズ検証だけ正しい」という半端な整合性を避けるための防御です。

### 6. metadata を引き継がない出力パス

- 変換前のピクセルは、一度 metadata-free な temp PNG に正規化されます。
- `cwebp` 呼び出しは `exec.CommandContext` を使い、shell を介さず、`-metadata none` を明示して実行します。

そのため、source container に載っていた metadata や ancillary chunks を WebP 出力へそのまま持ち込まない設計になっています。加えて、shell 展開に起因する command injection の面も持ち込みません。

### 7. Worker pool で並列化しつつ、全件一括ロードを避ける

- 走査結果は channel 経由で worker pool に流し込みます。
- `-workers` と `-cpus` で並列度を制御でき、`auto` でも上限を持たせています。
- 画像セット全体をメモリへ抱え込まず、1 ファイルずつ検証・変換します。

これにより、大量件数のディレクトリでも resource use を予測しやすく保ちます。

### 8. Symlink は既定で拒否し、許可時も root containment を維持

- symlink は既定で follow しません。
- `-follow-symlinks` を有効にした場合でも、解決先が要求された root の外へ出るものは `rejected_symlink_escape` として拒否します。
- symlink 経由で重複到達した directory は dedupe されます。

「既定は skip、明示 opt-in 時も root 外は reject」という二段構えで、ディレクトリトラバーサルや意図しない再帰を抑えます。

### 9. Hidden/System/VCS directory は既定で走査しない

- `.git`、`.hg`、`.svn`、`node_modules`、`$RECYCLE.BIN`、`System Volume Information` は既定で skip します。
- dot directory / dot file も `-include-hidden` を有効にしない限り対象外です。

CI や monorepo で「画像以外の場所まで無意識に掘る」事故を減らすための default-deny です。

### 10. 出力先は入力に追従するか、宣言された out-dir 配下へミラー

- 既定では元画像の隣に `.webp` を作成します。
- `-out-dir` 指定時は、入力の relative path を維持したミラー構造で出力します。
- walker は configured output directory を再走査しないように扱います。

出力パスは入力 relative path から決まるため、任意パス展開を伴う書き込みをしません。

### 11. Temp file + rename で staged output を commit

- encoder input と output は destination directory 配下の temp file に作られます。
- 実出力は、サイズや dimensions の検証が通った後に rename で commit されます。
- 上書き時は backup を使って rollback できるようにしています。

これにより、中断や encode 失敗の途中で最終ファイルが壊れた状態になることを避けやすくしています。

### 12. SIGINT / SIGTERM を context cancel に統一

- プロセスは `SIGINT` と `SIGTERM` を受けると context cancel を流します。
- copy、encode、publish、json write は context を尊重し、staged temp file を cleanup します。

長いバッチを止める時にも、「止まったが何が残ったか分からない」を減らす設計です。

### 13. Manifest / delivery path も containment で検証

- conversion manifest は source/output path を artifact-relative に保持し、machine 固有の絶対パスを前提にしません。
- `plan` と `verify` は manifest の path を root containment で再解決し、`../` を使った escape を reject します。
- public `release-manifest.json` は local filesystem path を露出しない形で組み立てられます。

変換フェーズだけでなく、artifact handoff と publish フェーズでも path discipline を維持します。

### 14. Publish 前後で hash と object key を再検証

- local publish は `upload local_path` の SHA-256 を再計算し、plan 内の hash と一致しない場合は upload しません。
- object key は正規化され、publish root の外へ出るものは reject されます。
- `verify-delivery` は plan に応じて content-type、cache-control、SHA-256 を確認できます。

これは「変換は正しかったが、配信で違うものを置いた」を減らすための検証です。

## CI/CD Integration

`webp-guard` を CI/CD へ組み込む場合は、次の運用を推奨します。

1. まず `doctor` で runtime readiness を確認する  
   `cwebp` の有無、temp dir、config discovery を先に失敗させると、後段の調査がかなり楽になります。

2. いきなり `bulk` せず、最初は `scan` で gate を張る  
   拒否や decode failure を「変換前のセキュリティシグナル」として扱い、review 対象にしてください。

3. CI では `-out-dir` を明示し、作業ツリーと artifact tree を分ける  
   source tree を read-mostly に保ちやすく、生成物の収集や破棄も単純になります。

4. `-follow-symlinks` は必要性が明確な時だけ有効にする  
   symlink を使う repo でも、まずは default の skip で回し、本当に必要なケースだけ opt-in するのが無難です。

5. 上限値を repo ごとに固定する  
   `max_file_size_mb`、`max_pixels`、`max_dimension` を config へ明示しておくと、開発者ごとの差が出にくくなります。

6. 変換後は `verify` を実行する  
   source drift、size regression、dimension mismatch を CI で再確認し、manifest と disk state の整合を取ってください。

7. publish 前に plan artifact を保存し、review 可能にする  
   `conversion-manifest.json`、`release-manifest.json`、`deploy-plan.json` を build artifact として残すと、差分レビューしやすくなります。

8. least privilege で実行する  
   root 権限ではなく、source directory と output directory だけに必要な権限を持つ専用ユーザーで動かすのが基本です。

9. version を pin する  
   CLI 本体と `cwebp` の version を固定し、パイプライン間で encoder 差分を持ち込まない運用を推奨します。

10. report は内部ログ、manifest は受け渡し artifact として扱い分ける  
    `report` の JSONL/CSV は運用調査向けで、環境によっては local path を含みます。外部共有や公開配布には `release-manifest.json` や `deploy-plan.json` のような artifact-relative な成果物を使うほうが適切です。

最小構成の例:

```yaml
- run: webp-guard doctor -json
- run: webp-guard scan --dir ./assets --report ./out/scan.jsonl
- run: webp-guard bulk --dir ./assets --out-dir ./out/assets --report ./out/conversion-report.jsonl --manifest ./out/conversion-manifest.json
- run: webp-guard verify --dir ./assets --manifest ./out/conversion-manifest.json --report ./out/verify-report.jsonl
```

`scan`、`bulk`、`verify` のいずれも reject / failure を返した場合は non-zero exit を gate として扱うのが基本です。

## Reporting a Vulnerability

脆弱性らしき挙動を見つけた場合は、公開 issue に詳細を書かないでください。再現手順、影響範囲、入力サンプル、期待結果、実際の結果を添えて、非公開チャネルでの連絡を優先してください。

- GitHub の Private Vulnerability Reporting が有効なら、そちらを使ってください。
- 専用窓口が未整備の場合は、公開 issue ではなく maintainer へ非公開で連絡してください。
- 可能なら、再現に必要な最小入力と `webp-guard version`、OS、`cwebp` version を含めてください。

リポジトリ運用としては、公開前に専用の security contact または GitHub Security Advisory workflow を明示することを推奨します。

## Scope And Limitations

`webp-guard` の現行スコープには、意図的に含めていないものがあります。

- マルウェア検査や antivirus 代替
- steganography 検出
- 画像内テキストの secret / PII 検出
- 画像の意味内容に対する moderation
- `cwebp` 自体の sandboxing
- すべての入力で WebP が必ず小さくなる保証

このツールが保証しようとしているのは、「危険な入力や危険な path を通しにくくし、失敗時に予測可能に止まり、artifact を検証可能な形で残すこと」です。そこから先の実行権限設計、supply chain 管理、秘密情報管理は、利用側のパイプライン設計に委ねられます。
