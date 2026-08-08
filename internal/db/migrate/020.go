package migrate

import (
	"fmt"

	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

// 阶段:请求压缩统计落库列。
//   - RelayLog.compress_saved_pct(0-100,NULL 表示未压缩)
//
// 只做 ADD COLUMN,绝不触发 glebarez/sqlite 的 AlterColumn→recreateTable
// 全表拷贝(沿用 015/016/017/018/019 的 HasColumn+AddColumn 幂等惯例)。
// relay_logs 表体积较大,SQLite ADD COLUMN 是元数据级操作、即时完成,
// 不需要回填(未压缩的旧行保持 NULL)。
func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 2026080801,
		Up:      migrateCompressSavedPct,
	})
}

func migrateCompressSavedPct(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable(&model.RelayLog{}) {
		return nil
	}
	if db.Migrator().HasColumn(&model.RelayLog{}, "compress_saved_pct") {
		return nil
	}
	if err := db.Migrator().AddColumn(&model.RelayLog{}, "CompressSavedPct"); err != nil {
		return fmt.Errorf("add relay_logs.compress_saved_pct: %w", err)
	}
	return nil
}
