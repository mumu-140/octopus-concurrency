package migrate

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newCompressPctTestDB 构造"旧 schema"relay_logs 表:存在但不含 compress_saved_pct,
// 模拟 v0.10.x 数据库升级到引入压缩统计列的路径。
func newCompressPctTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// 用裸 SQL 建旧表(不含新列),避免直接 AutoMigrate 当前 model 把新列一并建出来。
	if err := db.Exec(`CREATE TABLE relay_logs (
		id integer PRIMARY KEY AUTOINCREMENT,
		model_name text,
		input_tokens integer,
		output_tokens integer
	)`).Error; err != nil {
		t.Fatalf("create legacy relay_logs: %v", err)
	}
	return db
}

func TestMigrateCompressSavedPct(t *testing.T) {
	db := newCompressPctTestDB(t)

	// 播种一条旧日志,迁移后应保留且新列为 NULL。
	if err := db.Exec(`INSERT INTO relay_logs (model_name, input_tokens, output_tokens) VALUES ('m1', 10, 5)`).Error; err != nil {
		t.Fatalf("seed relay_logs: %v", err)
	}

	if err := migrateCompressSavedPct(db); err != nil {
		t.Fatalf("migrateCompressSavedPct: %v", err)
	}

	// 列存在。
	type row struct {
		CompressSavedPct *int
	}
	var got row
	if err := db.Table("relay_logs").Select("compress_saved_pct").Take(&got).Error; err != nil {
		t.Fatalf("read compress_saved_pct: %v", err)
	}
	if got.CompressSavedPct != nil {
		t.Errorf("compress_saved_pct = %v, want NULL (未压缩旧行)", *got.CompressSavedPct)
	}

	// 幂等:重复执行不报错。
	if err := migrateCompressSavedPct(db); err != nil {
		t.Fatalf("migrateCompressSavedPct second run: %v", err)
	}
}

func TestMigrateCompressSavedPctNilDB(t *testing.T) {
	if err := migrateCompressSavedPct(nil); err == nil {
		t.Fatal("expected error for nil db")
	}
}
