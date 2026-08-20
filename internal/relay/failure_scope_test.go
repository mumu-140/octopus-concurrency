package relay

import (
	"errors"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/outlierwindow"
)

// TestClassifyFailureScope 表驱动：报文与状态码取自生产日志（fwq57ys，48h 样本）。
// 若分类退回「一律按渠道计入」，Ignore/Model 用例会立即失败。
func TestClassifyFailureScope(t *testing.T) {
	cases := []struct {
		name   string
		status int
		text   string
		want   failureScope
	}{
		// 客户端请求本身不合法：换渠道也不会成功，不能算上游不健康。
		// 生产日志 48h 内出现 29228 次，是最大的误判来源。
		{"客户端-thinking参数非法", 400, `{"error":{"message":"invalid value at messages: validate_thinking_parts_role"}}`, scopeIgnore},
		{"客户端-缺必填参数", 400, `missing required parameter: 'model'`, scopeIgnore},
		{"客户端-不支持的参数", 400, `unsupported parameter: 'temperature'`, scopeIgnore},
		{"客户端-枚举值非法", 400, `'auto' is not one of ['low','high']`, scopeIgnore},

		// 渠道级：与具体模型无关，整渠道当前不可用。
		{"渠道-额度耗尽", 403, `{"error":{"code":"insufficient_user_quota","message":"用户额度不足"}}`, scopeChannel},
		{"渠道-令牌失效", 401, `Invalid token provided`, scopeChannel},
		{"渠道-无可用账号", 503, `No available accounts for this channel`, scopeChannel},
		{"渠道-认证错误", 400, `{"type":"authentication_error"}`, scopeChannel},
		{"渠道-连接被重置", 0, `read tcp 10.0.0.1:443: connection reset by peer`, scopeChannel},
		{"渠道-发送失败", 502, `failed to send request: dial tcp 1.2.3.4:443: i/o timeout`, scopeChannel},
		{"渠道-裸HTML拦截页", 405, `<html><head><title>405 Not Allowed</title></head></html>`, scopeChannel},
		{"渠道-Cloudflare拦截页", 403, `<!DOCTYPE html><title>Just a moment...</title>cf-ray: abc`, scopeChannel},
		{"渠道-无报文5xx", 524, ``, scopeChannel},
		{"渠道-无报文401", 401, ``, scopeChannel},
		{"渠道-连接层无状态码", 0, `context deadline exceeded (Client.Timeout exceeded while awaiting headers)`, scopeChannel},

		// 模型级：同渠道其它模型可能完全正常。
		{"模型-不存在", 503, `{"error":{"code":"model_not_found","message":"model not found"}}`, scopeModel},
		{"模型-无访问权限", 404, `The model 'gpt-5.6' does not exist or you do not have access to it`, scopeModel},
		{"模型-上游负载达上限", 500, `{"error":{"code":"get_channel_failed","message":"当前分组下模型负载已经达到上限"}}`, scopeModel},
		{"模型-限流", 429, `Rate limit reached for gpt-5.6 in organization org-x`, scopeModel},
		{"模型-未知4xx", 422, `unprocessable entity`, scopeModel},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyFailureScope(c.status, c.text); got != c.want {
				t.Fatalf("classifyFailureScope(%d, %q) = %d, want %d", c.status, c.text, got, c.want)
			}
		})
	}
}

// TestReportOutlierFailureIgnoreWritesNothing scopeIgnore 不得写入健康度窗口。
func TestReportOutlierFailureIgnoreWritesNothing(t *testing.T) {
	const ch = 90001
	outlierwindow.ClearChannel(ch)
	now := time.Now()

	reportOutlierFailure(ch, "m1", 400, `validate_thinking_parts_role`, now)

	if st := outlierwindow.Evaluate(ch, "m1", now); st.Samples != 0 {
		t.Fatalf("客户端错误不应写入健康度：Samples = %d, want 0", st.Samples)
	}
}

// TestReportOutlierFailureModelScopeOnlyCurrentModel scopeModel 只惩罚当前模型。
func TestReportOutlierFailureModelScopeOnlyCurrentModel(t *testing.T) {
	const ch = 90002
	outlierwindow.ClearChannel(ch)
	now := time.Now()
	// 先给 m2 建子键，确认它不会被 m1 的模型级失败牵连
	outlierwindow.Report(ch, "m2", true, 200, now)

	reportOutlierFailure(ch, "m1", 503, `model_not_found`, now)

	if st := outlierwindow.Evaluate(ch, "m1", now); st.Samples != 1 || st.Failures != 1 {
		t.Fatalf("m1: Samples=%d Failures=%d, want 1/1", st.Samples, st.Failures)
	}
	if st := outlierwindow.Evaluate(ch, "m2", now); st.Failures != 0 {
		t.Fatalf("m2 被模型级失败牵连：Failures = %d, want 0", st.Failures)
	}
}

// TestReportOutlierFailureChannelScopeFansOut scopeChannel 铺到该渠道全部已知模型。
func TestReportOutlierFailureChannelScopeFansOut(t *testing.T) {
	const ch = 90003
	outlierwindow.ClearChannel(ch)
	now := time.Now()
	outlierwindow.Report(ch, "m1", true, 200, now)
	outlierwindow.Report(ch, "m2", true, 200, now)

	reportOutlierFailure(ch, "m1", 403, `insufficient_user_quota`, now)

	for _, m := range []string{"m1", "m2"} {
		st := outlierwindow.Evaluate(ch, m, now)
		if st.Samples != 2 || st.Failures != 1 {
			t.Fatalf("%s: Samples=%d Failures=%d, want 2/1（渠道级失败未铺开）", m, st.Samples, st.Failures)
		}
	}
}

// TestOutlierErrorTextCombinesAndLowercases 拼接错误与上游报文并统一小写，
// 否则大写报文（如 "Invalid token"）无法命中 marker。
func TestOutlierErrorTextCombinesAndLowercases(t *testing.T) {
	got := outlierErrorText(errors.New("Upstream Error From Channel X"), `{"code":"Invalid Token"}`)
	if got != `upstream error from channel x {"code":"invalid token"}` {
		t.Fatalf("outlierErrorText = %q", got)
	}
	if classifyFailureScope(401, got) != scopeChannel {
		t.Fatal("大写报文小写化后应命中渠道级 marker")
	}
	if outlierErrorText(nil, "") != "" {
		t.Fatal("空输入应返回空串")
	}
}
