package handlers

import (
	"net/http"
	"strconv"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/protocol-routing").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(router.NewRoute("/policy", http.MethodGet).Handle(getProtocolPolicy)).
		AddRoute(router.NewRoute("/config", http.MethodPut).Handle(updateProtocolRoutingConfig)).
		AddRoute(router.NewRoute("/channels/:id", http.MethodGet).Handle(getChannelProtocolPolicy)).
		AddRoute(router.NewRoute("/channels/:id", http.MethodPut).Handle(replaceChannelProtocolPolicy)).
		AddRoute(router.NewRoute("/groups/:id", http.MethodPut).Handle(updateGroupProtocolPolicy)).
		AddRoute(router.NewRoute("/group-presets/:id", http.MethodPut).Handle(updateGroupPresetProtocolPolicy))
}

func getProtocolPolicy(c *gin.Context) {
	state, err := op.ProtocolPolicyGet(c.Request.Context())
	if err != nil {
		protocolRoutingHandlerError(c, err)
		return
	}
	resp.Success(c, state)
}

func updateProtocolRoutingConfig(c *gin.Context) {
	var req model.ProtocolRoutingConfigUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	state, err := op.ProtocolRoutingConfigUpdate(&req, protocolRoutingActor(c), c.Request.Context())
	if err != nil {
		protocolRoutingHandlerError(c, err)
		return
	}
	resp.Success(c, state)
}

func getChannelProtocolPolicy(c *gin.Context) {
	channelID, ok := protocolRoutingResourceID(c)
	if !ok {
		return
	}
	policy, err := op.ChannelProtocolPolicyGet(channelID, c.Request.Context())
	if err != nil {
		protocolRoutingHandlerError(c, err)
		return
	}
	state, err := op.ProtocolPolicyGet(c.Request.Context())
	if err != nil {
		protocolRoutingHandlerError(c, err)
		return
	}
	resp.Success(c, gin.H{"active_revision": state.ActiveRevision, "policy": policy})
}

func replaceChannelProtocolPolicy(c *gin.Context) {
	channelID, ok := protocolRoutingResourceID(c)
	if !ok {
		return
	}
	var req model.ChannelProtocolPolicyUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	state, err := op.ChannelProtocolPolicyReplace(channelID, &req, protocolRoutingActor(c), c.Request.Context())
	if err != nil {
		protocolRoutingHandlerError(c, err)
		return
	}
	resp.Success(c, state)
}

func updateGroupProtocolPolicy(c *gin.Context) {
	groupID, ok := protocolRoutingResourceID(c)
	if !ok {
		return
	}
	var req model.ScopedProtocolPolicyUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	state, err := op.GroupProtocolPolicyUpdate(groupID, &req, protocolRoutingActor(c), c.Request.Context())
	if err != nil {
		protocolRoutingHandlerError(c, err)
		return
	}
	resp.Success(c, state)
}

func updateGroupPresetProtocolPolicy(c *gin.Context) {
	presetID, ok := protocolRoutingResourceID(c)
	if !ok {
		return
	}
	var req model.ScopedProtocolPolicyUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	state, err := op.GroupPresetProtocolPolicyUpdate(presetID, &req, protocolRoutingActor(c), c.Request.Context())
	if err != nil {
		protocolRoutingHandlerError(c, err)
		return
	}
	resp.Success(c, state)
}

func protocolRoutingResourceID(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		resp.InvalidParam(c)
		return 0, false
	}
	return id, true
}

func protocolRoutingHandlerError(c *gin.Context, err error) {
	resp.ErrorWithAppError(c, http.StatusInternalServerError, err)
}

func protocolRoutingActor(_ *gin.Context) string {
	return "admin"
}
