package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	dbpkg "github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/gin-gonic/gin"
)

func TestProtocolRoutingConfigHandlersReturnSuccessAndConflict(t *testing.T) {
	setupProtocolRouteHandlerTestDB(t)

	readRecorder := httptest.NewRecorder()
	readContext, _ := gin.CreateTestContext(readRecorder)
	readContext.Request = httptest.NewRequest(http.MethodGet, "/api/v1/protocol-routing/policy", nil)
	getProtocolPolicy(readContext)
	if readRecorder.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", readRecorder.Code, readRecorder.Body.String())
	}

	updateBody := []byte(`{"expected_revision":0,"protocol_routing_enabled":true,"mode":"observe"}`)
	updateRecorder := httptest.NewRecorder()
	updateContext, _ := gin.CreateTestContext(updateRecorder)
	updateContext.Request = httptest.NewRequest(http.MethodPut, "/api/v1/protocol-routing/config", bytes.NewReader(updateBody))
	updateContext.Request.Header.Set("Content-Type", "application/json")
	updateProtocolRoutingConfig(updateContext)
	if updateRecorder.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", updateRecorder.Code, updateRecorder.Body.String())
	}

	conflictRecorder := httptest.NewRecorder()
	conflictContext, _ := gin.CreateTestContext(conflictRecorder)
	conflictContext.Request = httptest.NewRequest(http.MethodPut, "/api/v1/protocol-routing/config", bytes.NewReader(updateBody))
	conflictContext.Request.Header.Set("Content-Type", "application/json")
	updateProtocolRoutingConfig(conflictContext)
	if conflictRecorder.Code != http.StatusConflict {
		t.Fatalf("stale PUT status=%d body=%s", conflictRecorder.Code, conflictRecorder.Body.String())
	}
	var response resp.ResponseStruct
	if err := json.Unmarshal(conflictRecorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode conflict response: %v", err)
	}
	if response.ErrorCode != "protocol_routing.revision_conflict" {
		t.Fatalf("unexpected error code: %s", response.ErrorCode)
	}
}

func TestProtocolRoutingHandlersValidateJSONAndResourceIDs(t *testing.T) {
	setupProtocolRouteHandlerTestDB(t)

	badJSONRecorder := httptest.NewRecorder()
	badJSONContext, _ := gin.CreateTestContext(badJSONRecorder)
	badJSONContext.Request = httptest.NewRequest(http.MethodPut, "/api/v1/protocol-routing/config", bytes.NewBufferString("{"))
	badJSONContext.Request.Header.Set("Content-Type", "application/json")
	updateProtocolRoutingConfig(badJSONContext)
	if badJSONRecorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON status=%d body=%s", badJSONRecorder.Code, badJSONRecorder.Body.String())
	}

	missingRecorder := httptest.NewRecorder()
	missingContext, _ := gin.CreateTestContext(missingRecorder)
	missingContext.Request = httptest.NewRequest(http.MethodGet, "/api/v1/protocol-routing/channels/9999", nil)
	missingContext.Params = gin.Params{{Key: "id", Value: "9999"}}
	getChannelProtocolPolicy(missingContext)
	if missingRecorder.Code != http.StatusNotFound {
		t.Fatalf("missing channel status=%d body=%s", missingRecorder.Code, missingRecorder.Body.String())
	}

	invalidIDRecorder := httptest.NewRecorder()
	invalidIDContext, _ := gin.CreateTestContext(invalidIDRecorder)
	invalidIDContext.Request = httptest.NewRequest(http.MethodGet, "/api/v1/protocol-routing/channels/nope", nil)
	invalidIDContext.Params = gin.Params{{Key: "id", Value: "nope"}}
	getChannelProtocolPolicy(invalidIDContext)
	if invalidIDRecorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid id status=%d body=%s", invalidIDRecorder.Code, invalidIDRecorder.Body.String())
	}
}

func setupProtocolRouteHandlerTestDB(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	if dbpkg.GetDB() != nil {
		_ = dbpkg.Close()
	}
	dbPath := filepath.Join(t.TempDir(), "octopus-handler-test.db")
	if err := dbpkg.InitDB("sqlite", dbPath, false); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = dbpkg.Close() })
}
