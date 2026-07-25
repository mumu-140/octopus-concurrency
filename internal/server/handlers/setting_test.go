package handlers

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestParseOptionalBoolQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name      string
		query     string
		want      bool
		wantError bool
	}{
		{name: "missing", query: "", want: false},
		{name: "true", query: "?include_stats=true", want: true},
		{name: "false", query: "?include_stats=false", want: false},
		{name: "invalid", query: "?include_stats=treu", wantError: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("GET", "/"+testCase.query, nil)
			got, err := parseOptionalBoolQuery(ctx, "include_stats")
			if (err != nil) != testCase.wantError {
				t.Fatalf("error = %v, wantError %v", err, testCase.wantError)
			}
			if got != testCase.want {
				t.Fatalf("value = %v, want %v", got, testCase.want)
			}
		})
	}
}
