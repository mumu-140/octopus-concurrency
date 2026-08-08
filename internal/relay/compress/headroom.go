package compress

import (
	"encoding/json"
	"sort"
	"strings"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	transformerModel "github.com/bestruirui/octopus/internal/transformer/model"
)

const (
	headroomMinRows     = 8       // 少于此行数的 JSON 数组不转换
	headroomMinTextSize = 2000    // 短文本不做扫描
	headroomMaxSpanSize = 4 << 20 // 单个候选数组的最大扫描字节数
)

// headroomEngine 将消息文本中"同构扁平 JSON 数组"转换为列式文本表。
// 格式(octo-tabular): 围栏块内首行为列名 JSON 数组, 后续每行是一行数据的 JSON 数组。
// 信息无损: 键名只出现一次; 模型可直接阅读; 测试可解析回原行集。
// 借鉴 OmniRoute headroom 引擎(实测 30 行数组节省 71.2%)。
type headroomEngine struct{}

func (headroomEngine) Name() string { return "headroom" }

func (headroomEngine) Apply(msgs []transformerModel.Message, cfg *dbmodel.GroupCompressConfig) []transformerModel.Message {
	if !cfg.Headroom {
		return msgs
	}
	forEachText(msgs, func(_, text string) string {
		if len(text) < headroomMinTextSize {
			return text
		}
		out := convertJSONArrays(text)
		// 保真兜底: 只在更短时替换
		if len(out) < len(text) {
			return out
		}
		return text
	})
	return msgs
}

// convertJSONArrays 扫描文本中的同构 JSON 数组并替换为 octo-tabular 块。
// 代码围栏内部的数组不处理。
func convertJSONArrays(text string) string {
	fenceRanges := fencedByteRanges(text)
	var b strings.Builder
	b.Grow(len(text))
	pos := 0
	for pos < len(text) {
		i := strings.IndexByte(text[pos:], '[')
		if i < 0 {
			break
		}
		start := pos + i
		if inRanges(fenceRanges, start) || !looksLikeObjectArray(text, start) {
			b.WriteString(text[pos : start+1])
			pos = start + 1
			continue
		}
		end := matchBracket(text, start)
		if end < 0 || end-start > headroomMaxSpanSize {
			b.WriteString(text[pos : start+1])
			pos = start + 1
			continue
		}
		repl, ok := renderTabular(text[start : end+1])
		if !ok {
			b.WriteString(text[pos : start+1])
			pos = start + 1
			continue
		}
		b.WriteString(text[pos:start])
		b.WriteString(repl)
		pos = end + 1
	}
	b.WriteString(text[pos:])
	return b.String()
}

// looksLikeObjectArray 快速预判: '[' 后第一个非空白字符是否为 '{'。
func looksLikeObjectArray(text string, start int) bool {
	for i := start + 1; i < len(text); i++ {
		switch text[i] {
		case ' ', '\t', '\n', '\r':
			continue
		case '{':
			return true
		default:
			return false
		}
	}
	return false
}

// matchBracket 找到与 text[start]='[' 匹配的 ']' 下标(字符串字面量感知)。找不到返回 -1。
func matchBracket(text string, start int) int {
	depth := 0
	inStr := false
	escaped := false
	for i := start; i < len(text); i++ {
		c := text[i]
		if inStr {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '[', '{':
			depth++
		case ']', '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// renderTabular 校验同构性并渲染 octo-tabular 块。键按字典序排列(确定性、无损)。
func renderTabular(raw string) (string, bool) {
	var rows []map[string]any
	if err := json.Unmarshal([]byte(raw), &rows); err != nil || len(rows) < headroomMinRows {
		return "", false
	}
	keys := make([]string, 0, len(rows[0]))
	for k := range rows[0] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return "", false
	}
	keySet := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		keySet[k] = struct{}{}
	}
	for _, row := range rows {
		if len(row) != len(keys) {
			return "", false
		}
		for k, v := range row {
			if _, ok := keySet[k]; !ok {
				return "", false
			}
			if !isScalar(v) {
				return "", false
			}
		}
	}

	var b strings.Builder
	b.WriteString("```octo-tabular\n")
	header, _ := json.Marshal(keys)
	b.Write(header)
	b.WriteByte('\n')
	cells := make([]any, len(keys))
	for _, row := range rows {
		for i, k := range keys {
			cells[i] = row[k]
		}
		line, err := json.Marshal(cells)
		if err != nil {
			return "", false
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	b.WriteString("```")
	return b.String(), true
}

// isScalar 只允许扁平标量值(string/number/bool/null)。
func isScalar(v any) bool {
	switch v.(type) {
	case nil, string, bool, float64, json.Number:
		return true
	default:
		return false
	}
}

// fencedByteRanges 返回代码围栏的字节区间(行级判定, 与 lite 引擎同规则)。
func fencedByteRanges(text string) [][2]int {
	var ranges [][2]int
	fenced := false
	start := 0
	pos := 0
	for pos <= len(text) {
		nl := strings.IndexByte(text[pos:], '\n')
		var line string
		var next int
		if nl < 0 {
			line = text[pos:]
			next = len(text) + 1
		} else {
			line = text[pos : pos+nl]
			next = pos + nl + 1
		}
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "```") {
			if !fenced {
				fenced = true
				start = pos
			} else {
				fenced = false
				ranges = append(ranges, [2]int{start, next})
			}
		}
		pos = next
	}
	if fenced {
		ranges = append(ranges, [2]int{start, len(text)})
	}
	return ranges
}

func inRanges(ranges [][2]int, i int) bool {
	for _, r := range ranges {
		if i >= r[0] && i < r[1] {
			return true
		}
	}
	return false
}

// tabularMarker 供测试与调试识别转换结果。
const tabularMarker = "```octo-tabular"
