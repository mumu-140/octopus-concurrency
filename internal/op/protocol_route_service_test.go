package op

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"testing"

	"github.com/bestruirui/octopus/internal/apperror"
	dbpkg "github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/protocolroute"
)

func TestProtocolRoutingConfigUpdateUsesRevisionCAS(t *testing.T) {
	ctx := setupSiteOpTestDB(t)

	initial, err := ProtocolPolicyGet(ctx)
	if err != nil {
		t.Fatalf("get initial policy: %v", err)
	}
	if initial.ActiveRevision != 0 || initial.Config.Mode != model.ProtocolRoutingModeLegacy {
		t.Fatalf("unexpected initial policy: %+v", initial)
	}

	updated, err := ProtocolRoutingConfigUpdate(&model.ProtocolRoutingConfigUpdateRequest{
		ExpectedRevision:          0,
		ProtocolRoutingEnabled:    testBoolPtr(true),
		Mode:                      testRoutingModePtr(model.ProtocolRoutingModeObserve),
		ProtocolFallbackEnabled:   testBoolPtr(false),
		ProtocolConversionEnabled: testBoolPtr(true),
		RankingSignalOrder:        &[]string{"ingress", "group_prefer", "channel_type"},
	}, "admin", ctx)
	if err != nil {
		t.Fatalf("update config: %v", err)
	}
	if updated.ActiveRevision != 1 || updated.Config.Mode != model.ProtocolRoutingModeObserve {
		t.Fatalf("unexpected updated policy: %+v", updated)
	}
	if !protocolroute.ObserveEnabled() {
		t.Fatal("observe projection was not activated after commit")
	}

	_, err = ProtocolRoutingConfigUpdate(&model.ProtocolRoutingConfigUpdateRequest{
		ExpectedRevision: 0,
		Mode:             testRoutingModePtr(model.ProtocolRoutingModeLegacy),
	}, "stale-admin", ctx)
	if apperror.Code(err) != CodeProtocolRoutingConflict {
		t.Fatalf("expected conflict, got %v (%s)", err, apperror.Code(err))
	}
	if apperror.Status(err) != 409 {
		t.Fatalf("expected HTTP 409, got %d", apperror.Status(err))
	}

	assertProtocolRevisionIntegrity(t, ctx, 1, 1)
}

func TestProtocolRoutingConfigConcurrentCASAllowsOneWriter(t *testing.T) {
	ctx := setupSiteOpTestDB(t)

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := ProtocolRoutingConfigUpdate(&model.ProtocolRoutingConfigUpdateRequest{
				ExpectedRevision: 0,
				Mode:             testRoutingModePtr(model.ProtocolRoutingModeObserve),
			}, "admin", ctx)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case apperror.Code(err) == CodeProtocolRoutingConflict:
			conflicts++
		default:
			t.Fatalf("unexpected concurrent result: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("expected one success and one conflict, got success=%d conflict=%d", successes, conflicts)
	}
	assertProtocolRevisionIntegrity(t, ctx, 1, 1)
}

func testBoolPtr(value bool) *bool { return &value }

func testRoutingModePtr(value model.ProtocolRoutingMode) *model.ProtocolRoutingMode {
	return &value
}

func assertProtocolRevisionIntegrity(t *testing.T, ctx context.Context, wantActive, wantRows int64) {
	t.Helper()
	var cfg model.ProtocolRoutingConfig
	if err := dbpkg.GetDB().WithContext(ctx).First(&cfg, 1).Error; err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.ActiveRevision != wantActive {
		t.Fatalf("active revision=%d, want %d", cfg.ActiveRevision, wantActive)
	}
	var revisions []model.ProtocolPolicyRevision
	if err := dbpkg.GetDB().WithContext(ctx).Order("revision ASC").Find(&revisions).Error; err != nil {
		t.Fatalf("load revisions: %v", err)
	}
	if int64(len(revisions)) != wantRows {
		t.Fatalf("revision rows=%d, want %d", len(revisions), wantRows)
	}
	for _, revision := range revisions {
		sum := sha256.Sum256([]byte(revision.PayloadJSON))
		if revision.PayloadSHA256 != hex.EncodeToString(sum[:]) {
			t.Fatalf("revision %d payload hash mismatch", revision.Revision)
		}
	}
}
