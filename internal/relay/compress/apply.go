package compress

import (
	"time"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	transformerModel "github.com/bestruirui/octopus/internal/transformer/model"
	"github.com/bestruirui/octopus/internal/utils/log"
)

// masterEnabledFn 全局急停开关读取，包级变量以便测试注入。
// 生产路径走 settingCache（内存缓存），单次读取为 ns 级。
var masterEnabledFn = func() bool {
	v, err := op.SettingGetBool(dbmodel.SettingKeyCompressMasterEnabled)
	return err == nil && v
}

// MaybeApply 在分组启用且全局 master 开启时对请求消息执行压缩引擎链。
// 关闭路径仅做几次廉价判断，零分配。任何引擎失败均不影响请求转发。
//
// 挂点：relay_handler.go 分组解析之后、转发之前。
// 注意：仅处理 Messages 形态请求；RawInputItems(Responses API) 形态暂不支持，直接跳过。
func MaybeApply(req *transformerModel.InternalLLMRequest, group dbmodel.Group) {
	if req == nil || len(req.Messages) == 0 {
		return
	}
	cfg := group.CompressConfig
	if cfg == nil || !cfg.Enabled {
		return
	}
	if !masterEnabledFn() {
		return
	}

	before := messagesSize(req.Messages)
	start := time.Now()
	msgs := req.Messages
	for _, e := range engines {
		var s Stats
		msgs, s = runEngine(e, msgs, cfg)
		if s.Err != "" {
			log.Warnf("compress: engine %s failed: %s", s.Engine, s.Err)
			continue
		}
		if s.Applied {
			log.Debugf("compress: engine=%s before=%d after=%d elapsed=%s",
				s.Engine, s.BytesBefore, s.BytesAfter, s.Elapsed)
		}
	}
	req.Messages = msgs

	after := messagesSize(req.Messages)
	if after < before {
		log.Debugf("compress: group=%d saved=%.1f%% before=%d after=%d elapsed=%s",
			group.ID, float64(before-after)/float64(before)*100, before, after, time.Since(start))
		// 暂存压缩统计到请求对象上,relay metrics 落库日志时读取(json:"-" 不外发)。
		req.CompressStats = &transformerModel.CompressStats{BeforeBytes: before, AfterBytes: after}
	}
}
