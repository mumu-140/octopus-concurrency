package compress

import (
	"fmt"
	"time"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	transformerModel "github.com/bestruirui/octopus/internal/transformer/model"
	"github.com/bestruirui/octopus/internal/utils/log"
)

// Stats 单个引擎一次执行的统计。
type Stats struct {
	Engine      string
	Applied     bool
	BytesBefore int
	BytesAfter  int
	Elapsed     time.Duration
	Err         string
}

// Engine 单个压缩引擎。实现约束：
//   - 只修改消息文本内容
//   - 幂等（重复应用结果不变）
//   - 返回新的消息切片（可能增删消息）
type Engine interface {
	Name() string
	Apply(msgs []transformerModel.Message, cfg *dbmodel.GroupCompressConfig) []transformerModel.Message
}

// engines 引擎链，按序执行。包级变量以便测试注入故障引擎。
var engines = []Engine{
	liteEngine{},
	headroomEngine{},
	outputStyleEngine{},
}

// runEngine fail-open 包装：recover panic + 统计。panic 时返回入参原切片。
func runEngine(e Engine, msgs []transformerModel.Message, cfg *dbmodel.GroupCompressConfig) (out []transformerModel.Message, s Stats) {
	out = msgs
	s.Engine = e.Name()
	s.BytesBefore = messagesSize(msgs)
	start := time.Now()
	defer func() {
		if r := recover(); r != nil {
			log.Warnf("compress: engine %s panic recovered: %v", e.Name(), r)
			out = msgs
			s = Stats{Engine: e.Name(), BytesBefore: s.BytesBefore, BytesAfter: s.BytesBefore, Err: fmt.Sprintf("panic: %v", r)}
		}
	}()
	out = e.Apply(msgs, cfg)
	s.BytesAfter = messagesSize(out)
	s.Applied = s.BytesAfter != s.BytesBefore
	s.Elapsed = time.Since(start)
	return out, s
}

// messagesSize 汇总所有消息文本段长度。
func messagesSize(msgs []transformerModel.Message) int {
	n := 0
	forEachText(msgs, func(_, text string) string {
		n += len(text)
		return text
	})
	return n
}

// forEachText 遍历每条消息的全部文本段（纯文本或多模态 text part），用 fn 返回值替换。
func forEachText(msgs []transformerModel.Message, fn func(role, text string) string) {
	for i := range msgs {
		m := &msgs[i]
		if m.Content.Content != nil {
			*m.Content.Content = fn(m.Role, *m.Content.Content)
			continue
		}
		for j := range m.Content.MultipleContent {
			p := &m.Content.MultipleContent[j]
			if p.Type == "text" && p.Text != nil {
				*p.Text = fn(m.Role, *p.Text)
			}
		}
	}
}
