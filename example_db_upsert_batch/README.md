# example_db_upsert_batch

`UPSERT` を DB とつなぐ現場向けに、Go の標準 `database/sql` だけで持ち運べる最小サンプルを置いています。

ここで大事なのはひとつで、`UPSERT` は完全な汎用 SQL ではありません。

- PostgreSQL / SQLite: `INSERT ... ON CONFLICT ... DO UPDATE`
- MySQL / MariaDB: `INSERT ... ON DUPLICATE KEY UPDATE`

なので、このディレクトリでは

- 並列 worker
- batched multi-row insert
- エラー伝播
- `database/sql` ベースの呼び出し方

を共通化し、SQL 方言だけを `Dialect` で切り替える形にしています。

## 何が入っているか

- `upsert_batch.go`: batched parallel upsert の本体
- `upsert_batch_test.go`: query 生成と batch 実行のテスト

## 使い方

`Row` の値順は `InsertColumns` と揃えてください。

```go
package main

import (
	"context"
	"database/sql"
	"time"

	batch "github.com/mt4110/webp-guard/example_db_upsert_batch"
)

func main() {
	db, err := sql.Open("postgres", "postgres://app:pass@localhost:5432/app?sslmode=disable")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	cfg := batch.Config{
		Dialect:         batch.DialectPostgres,
		Table:           "asset_variants",
		InsertColumns:   []string{"logical_path", "source_etag", "webp_key", "width", "height", "updated_at"},
		ConflictColumns: []string{"logical_path"},
		BatchSize:       250,
		Workers:         4,
	}

	rows := []batch.Row{
		{"images/hero.jpg", "etag-001", "assets/hero.webp", 1200, 800, time.Now().UTC()},
		{"images/card.jpg", "etag-002", "assets/card.webp", 960, 640, time.Now().UTC()},
	}

	if err := batch.UpsertRows(context.Background(), db, cfg, rows); err != nil {
		panic(err)
	}
}
```

このサンプル自体は driver を抱え込まないので、実際に使うときは対象 DB の driver import を足してください。

例:

- PostgreSQL: `github.com/lib/pq` または `github.com/jackc/pgx/v5/stdlib`
- MySQL / MariaDB: `github.com/go-sql-driver/mysql`
- SQLite: `modernc.org/sqlite` または `github.com/mattn/go-sqlite3`

## 方言ごとの SQL 例

同じ `Config` / `Row` から、方言ごとにだいたい次の形が生成されます。

PostgreSQL / SQLite:

```sql
INSERT INTO asset_variants (logical_path, source_etag, webp_key)
VALUES (?, ?, ?), (?, ?, ?)
ON CONFLICT (logical_path) DO UPDATE SET
source_etag = excluded.source_etag,
webp_key = excluded.webp_key
```

注: PostgreSQL 実行時の placeholder は実際には `$1, $2, ...` です。

MySQL / MariaDB:

```sql
INSERT INTO asset_variants (logical_path, source_etag, webp_key)
VALUES (?, ?, ?), (?, ?, ?)
ON DUPLICATE KEY UPDATE
source_etag = VALUES(source_etag),
webp_key = VALUES(webp_key)
```

## カスタマイズの勘所

- table 名は予約語を避ける
- 識別子は unquoted の `snake_case` に寄せる
- `ConflictColumns` は unique key / primary key に合わせる
- `UpdateColumns` を空にすると、conflict key 以外の列を自動で更新対象にする
- worker を増やしすぎると DB 側で詰まるので、まずは `2-4` から始める
- 大きな payload を入れるときは `BatchSize` を下げる

## この repo との距離感

`webp-guard` 本体は現時点では DB 前提ではなく、report / manifest を file artifact として残す設計です。
このディレクトリは、その結果をアプリ側の DB へ反映したいときの橋渡し用サンプルです。
