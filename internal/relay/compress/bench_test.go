package compress

import (
	"strings"
	"testing"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	transformerModel "github.com/bestruirui/octopus/internal/transformer/model"
)

// BenchmarkMaybeApplyDisabled 关闭路径预算: <1μs, 零分配。
func BenchmarkMaybeApplyDisabled(b *testing.B) {
	withMaster(b, true)
	req := buildRequest(padTo("payload\n\n\n\n", liteMinTextChars+100))
	g := dbmodel.Group{ID: 1} // nil 配置
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		MaybeApply(req, g)
	}
}

func BenchmarkLiteLargeToolOutput(b *testing.B) {
	body := "head\n" + strings.Repeat("some log line with trailing spaces   \n", 3000) + "tail summary\n"
	cfg := liteCfg()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cp := []transformerModel.Message{textMsg("tool", body)}
		_ = liteEngine{}.Apply(cp, cfg)
	}
}

func BenchmarkHeadroomJSONArray(b *testing.B) {
	text := "data:\n" + buildJSONArray(200)
	cfg := headroomCfg()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cp := []transformerModel.Message{textMsg("user", text)}
		_ = headroomEngine{}.Apply(cp, cfg)
	}
}
