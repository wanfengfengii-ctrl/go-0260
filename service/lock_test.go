package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"cacaoferment/catalog"
	"cacaoferment/service"
	"cacaoferment/store"
)

func TestLockSuccess(t *testing.T) {
	svc := newTestService(t, store.NewMemory())
	id := mustLock(t, svc)
	v, err := svc.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(v.Leases) != 5 { // 2 bins + 1 probe + 1 well + 1 window
		t.Fatalf("expected 5 leases, got %d", len(v.Leases))
	}
	if v.Task.State != "pending_boxing" {
		t.Fatalf("unexpected state %q", v.Task.State)
	}
}

func TestLockConcurrentSameResource(t *testing.T) {
	svc := newTestService(t, store.NewMemory())
	const n = 24
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := svc.Lock(context.Background(), defaultLock())
			errs[i] = err
		}(i)
	}
	close(start)
	wg.Wait()

	winners := 0
	for _, err := range errs {
		if err == nil {
			winners++
			continue
		}
		var ce *service.CodedError
		if !errors.As(err, &ce) || ce.Code != service.CodeResourceOccupied {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("expected exactly one winner, got %d", winners)
	}
}

func TestLockValidationErrors(t *testing.T) {
	svc := newTestService(t, store.NewMemory())
	cases := []struct {
		name string
		mut  func(*service.LockRequest)
		code string
	}{
		{"unknown rule", func(r *service.LockRequest) { r.RuleVersion = "v9" }, service.CodeUnknownRuleVersion},
		{"plot variety mismatch", func(r *service.LockRequest) { r.VarietyBatchID = "variety-9" }, service.CodePlotVarietyMismatch},
		{"invalid weight", func(r *service.LockRequest) { r.BoxedWeightGrams = 0 }, service.CodeInvalidWeight},
		{"duplicate bin", func(r *service.LockRequest) { r.BinIDs = []string{"bin-1", "bin-1"} }, service.CodeDuplicateBin},
		{"duplicate blind", func(r *service.LockRequest) {
			r.BlindSamples = append(r.BlindSamples, service.BlindSampleSpec{BlindCode: "BC-1", SampleSize: 50, BinID: "bin-1"})
		}, service.CodeDuplicateBlind},
		{"unknown equipment", func(r *service.LockRequest) { r.ProbeIDs = []string{"probe-9"} }, service.CodeUnknownEquipment},
		{"blind references unlocked bin", func(r *service.LockRequest) {
			r.BlindSamples = []service.BlindSampleSpec{{BlindCode: "BC-9", SampleSize: 50, BinID: "bin-9"}}
		}, service.CodeInvalidRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := defaultLock()
			c.mut(&req)
			_, err := svc.Lock(context.Background(), req)
			var ce *service.CodedError
			if !errors.As(err, &ce) {
				t.Fatalf("expected coded error, got %v", err)
			}
			if ce.Code != c.code {
				t.Fatalf("code = %q, want %q", ce.Code, c.code)
			}
		})
	}
}

// fixedCatalog is a minimal RuleCatalog used to inject a non-increasing turn
// schedule that would never pass AddSnapshot validation.
type fixedCatalog struct {
	snap        catalog.RuleSnapshot
	plotVariety bool
}

func (f fixedCatalog) Snapshot(v catalog.ThresholdVersion) (catalog.RuleSnapshot, bool) {
	return f.snap, v == f.snap.RuleVersion
}
func (f fixedCatalog) MatchPlotVariety(a, b string) bool { return f.plotVariety }

func TestLockNonIncreasingTurnNodes(t *testing.T) {
	c, _ := testSeed()
	// Build an invalid snapshot by hand (bypassing AddSnapshot validation).
	var snap catalog.RuleSnapshot
	snap, _ = c.Snapshot("v1")
	snap.TurnNodes = []int{1, 1, 3}
	fc := fixedCatalog{snap: snap, plotVariety: true}
	svc := service.New(store.NewMemory(), fc, nil, nil)
	_, err := svc.Lock(context.Background(), defaultLock())
	var ce *service.CodedError
	if !errors.As(err, &ce) || ce.Code != service.CodeInvalidRequest {
		t.Fatalf("expected invalid_request for non-increasing turn nodes, got %v", err)
	}
}
