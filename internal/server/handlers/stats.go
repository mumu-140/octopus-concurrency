package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/stats").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/today", http.MethodGet).
				Handle(getStatsToday),
		).
		AddRoute(
			router.NewRoute("/daily", http.MethodGet).
				Handle(getStatsDaily),
		).
		AddRoute(
			router.NewRoute("/hourly", http.MethodGet).
				Handle(getStatsHourly),
		).
		AddRoute(
			router.NewRoute("/total", http.MethodGet).
				Handle(getStatsTotal),
		).
		AddRoute(
			router.NewRoute("/apikey", http.MethodGet).
				Handle(getStatsAPIKey),
		).
		AddRoute(
			router.NewRoute("/leaderboard", http.MethodGet).
				Handle(getStatsLeaderboard),
		)
}

func getStatsToday(c *gin.Context) {
	resp.Success(c, op.StatsTodayGet())
}

func getStatsDaily(c *gin.Context) {
	statsDaily, err := op.StatsGetDaily(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, statsDaily)
}

func getStatsHourly(c *gin.Context) {
	resp.Success(c, op.StatsHourlyGet())
}

func getStatsTotal(c *gin.Context) {
	resp.Success(c, op.StatsTotalGet())
}

func getStatsAPIKey(c *gin.Context) {
	resp.Success(c, op.StatsAPIKeyList())
}

type leaderboardRow struct {
	Key           string  `json:"key"`
	Name          string  `json:"name"`
	InputToken    int64   `json:"input_token"`
	OutputToken   int64   `json:"output_token"`
	InputCost     float64 `json:"input_cost"`
	OutputCost    float64 `json:"output_cost"`
	WaitTime      int64   `json:"wait_time"`
	RequestSuccess int64  `json:"request_success"`
	RequestFailed int64   `json:"request_failed"`
	LastRequestAt int64   `json:"last_request_at"`
}

// getStatsLeaderboard 返回渠道 / 分组维度在给定时间窗口内的聚合排行数据。
// query: mode=channel|group（默认 channel），window=1|7|30|all（默认 all）。
// 排序与指标选择交由前端处理，与现有排行榜一致。
func getStatsLeaderboard(c *gin.Context) {
	dimType := model.StatsDimChannel
	if c.Query("mode") == "group" {
		dimType = model.StatsDimGroup
	}

	var lookback time.Duration
	switch c.Query("window") {
	case "1":
		lookback = 24 * time.Hour
	case "7":
		lookback = 7 * 24 * time.Hour
	case "30":
		lookback = 30 * 24 * time.Hour
	default:
		lookback = 0 // all-time
	}

	ctx := c.Request.Context()
	rows, err := op.StatsDimensionHourlyWindow(ctx, dimType, lookback)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	out := make([]leaderboardRow, 0, len(rows))
	for _, r := range rows {
		name := r.DimensionKey
		if dimType == model.StatsDimChannel {
			if id, convErr := strconv.Atoi(r.DimensionKey); convErr == nil {
				if ch, chErr := op.ChannelGet(id, ctx); chErr == nil {
					name = ch.Name
				}
			}
		}
		out = append(out, leaderboardRow{
			Key:            r.DimensionKey,
			Name:           name,
			InputToken:     r.InputToken,
			OutputToken:    r.OutputToken,
			InputCost:      r.InputCost,
			OutputCost:     r.OutputCost,
			WaitTime:       r.WaitTime,
			RequestSuccess: r.RequestSuccess,
			RequestFailed:  r.RequestFailed,
			LastRequestAt:  r.LastRequestAt,
		})
	}
	resp.Success(c, out)
}
