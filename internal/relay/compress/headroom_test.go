package compress

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	transformerModel "github.com/bestruirui/octopus/internal/transformer/model"
)

func headroomCfg() *dbmodel.GroupCompressConfig {
	return &dbmodel.GroupCompressConfig{Enabled: true, Headroom: true}
}

// buildJSONArray 生成 n 行同构 JSON 数组文本。
func buildJSONArray(n int) string {
	var b strings.Builder
	b.WriteString("[\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `  {"host": "server-%02d", "cpu": %.1f, "mem": %.1f, "ok": true}`, i, float64(i%100)+0.5, float64(i%64)+0.25)
		if i < n-1 {
			b.WriteString(",\n")
		}
	}
	b.WriteString("\n]")
	return b.String()
}

// parseTabular 把 octo-tabular 块解析回行集（round-trip 验证）。
func parseTabular(t *testing.T, block string) []map[string]any {
	t.Helper()
	lines := strings.Split(block, "\n")
	if len(lines) < 4 || lines[0] != tabularMarker {
		t.Fatalf("invalid tabular block header: %q", lines[0])
	}
	var header []string
	if err := json.Unmarshal([]byte(lines[1]), &header); err != nil {
		t.Fatalf("bad header line: %v", err)
	}
	var rows []map[string]any
	for _, line := range lines[2 : len(lines)-1] {
		var cells []any
		if err := json.Unmarshal([]byte(line), &cells); err != nil {
			t.Fatalf("bad row line %q: %v", line, err)
		}
		if len(cells) != len(header) {
			t.Fatalf("row width %d != header width %d", len(cells), len(header))
		}
		row := make(map[string]any, len(header))
		for i, k := range header {
			row[k] = cells[i]
		}
		rows = append(rows, row)
	}
	return rows
}

// normalizeRows 经 JSON 往返规范化原始数组文本，供与解析结果比较。
func normalizeRows(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		t.Fatalf("bad source array: %v", err)
	}
	return rows
}

func extractTabularBlock(t *testing.T, text string) string {
	t.Helper()
	start := strings.Index(text, tabularMarker)
	if start < 0 {
		t.Fatalf("tabular marker not found")
	}
	end := strings.Index(text[start+len(tabularMarker):], "\n```")
	if end < 0 {
		t.Fatalf("tabular closing fence not found")
	}
	return text[start : start+len(tabularMarker)+end+len("\n```")]
}

func TestHeadroomConvertsUniformArray(t *testing.T) {
	raw := buildJSONArray(60)
	text := "Here is the metrics data:\n" + raw + "\nPlease summarize."
	msgs := []transformerModel.Message{textMsg("user", text)}
	out := headroomEngine{}.Apply(msgs, headroomCfg())
	got := msgText(out[0])
	if !strings.Contains(got, tabularMarker) {
		t.Fatalf("expected tabular conversion")
	}
	if !strings.Contains(got, "Here is the metrics data:") || !strings.Contains(got, "Please summarize.") {
		t.Fatalf("surrounding prose must be preserved")
	}
	saved := float64(len(text)-len(got)) / float64(len(text)) * 100
	t.Logf("saved=%.1f%% before=%d after=%d", saved, len(text), len(got))
	if len(got) >= len(text) {
		t.Fatalf("expected reduction")
	}
}

func TestHeadroomRoundTrip(t *testing.T) {
	raw := buildJSONArray(60)
	msgs := []transformerModel.Message{textMsg("user", "data:\n" + raw)}
	out := headroomEngine{}.Apply(msgs, headroomCfg())
	block := extractTabularBlock(t, msgText(out[0]))
	parsed := parseTabular(t, block)
	expect := normalizeRows(t, raw)
	if !reflect.DeepEqual(parsed, expect) {
		t.Fatalf("round-trip mismatch:\nparsed[0]=%v\nexpect[0]=%v", parsed[0], expect[0])
	}
}

func TestHeadroomSkipsNonUniform(t *testing.T) {
	raw := `[{"a": 1}, {"a": 2, "b": 3}, {"a": 4}, {"a": 5}, {"a": 6}, {"a": 7}, {"a": 8}, {"a": 9}]`
	text := strings.Repeat("prefix prose line\n", 200) + raw
	msgs := []transformerModel.Message{textMsg("user", text)}
	out := headroomEngine{}.Apply(msgs, headroomCfg())
	if strings.Contains(msgText(out[0]), tabularMarker) {
		t.Fatalf("non-uniform rows must not be converted")
	}
}

func TestHeadroomSkipsSmallArray(t *testing.T) {
	raw := buildJSONArray(3)
	text := strings.Repeat("prefix prose line\n", 200) + raw
	msgs := []transformerModel.Message{textMsg("user", text)}
	out := headroomEngine{}.Apply(msgs, headroomCfg())
	if strings.Contains(msgText(out[0]), tabularMarker) {
		t.Fatalf("array smaller than min rows must not be converted")
	}
}

func TestHeadroomSkipsFencedArray(t *testing.T) {
	raw := buildJSONArray(20)
	text := "example:\n```json\n" + raw + "\n```\n" + strings.Repeat("tail prose\n", 200)
	msgs := []transformerModel.Message{textMsg("user", text)}
	out := headroomEngine{}.Apply(msgs, headroomCfg())
	if strings.Contains(msgText(out[0]), tabularMarker) {
		t.Fatalf("fenced array must not be converted")
	}
}

func TestHeadroomSkipsNestedValues(t *testing.T) {
	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < 10; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"id": %d, "meta": {"x": %d}}`, i, i)
	}
	b.WriteString("]")
	text := strings.Repeat("prose\n", 300) + b.String()
	msgs := []transformerModel.Message{textMsg("user", text)}
	out := headroomEngine{}.Apply(msgs, headroomCfg())
	if strings.Contains(msgText(out[0]), tabularMarker) {
		t.Fatalf("nested values must not be converted")
	}
}

func TestHeadroomIdempotent(t *testing.T) {
	raw := buildJSONArray(60)
	msgs := []transformerModel.Message{textMsg("user", "data:\n" + raw)}
	once := headroomEngine{}.Apply(msgs, headroomCfg())
	twice := headroomEngine{}.Apply(once, headroomCfg())
	if msgText(once[0]) != msgText(twice[0]) {
		t.Fatalf("headroom must be idempotent")
	}
}
