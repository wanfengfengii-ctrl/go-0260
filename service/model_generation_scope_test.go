package service_test

import (
	"context"
	"errors"
	"testing"

	"cacaoferment/arbiter"
	"cacaoferment/evidence"
	"cacaoferment/service"
	"cacaoferment/store"
	"cacaoferment/task"
)

func TestModel_CurrentGenerationEvidenceAfterRejudgement(t *testing.T) {
	ctx := context.Background()

	setupThroughCollection := func(t *testing.T, svc *service.Service) string {
		t.Helper()
		id := mustLock(t, svc)
		box(t, svc, id, "reviewer-a")
		box(t, svc, id, "reviewer-b")
		startEquipment(t, svc, id)
		collect(t, svc, id)
		return id
	}
	cut := func(t *testing.T, svc *service.Service, id, code, op string, generation int64) service.BlindSampleResult {
		t.Helper()
		score := arbiterCutScore()
		res, err := svc.SubmitBlindSample(ctx, service.BlindSampleRequest{
			TaskID: id, BlindCode: code, OperationKey: op, Generation: generation, CutScore: &score,
		})
		if err != nil {
			t.Fatalf("cut %s gen%d: %v", code, generation, err)
		}
		return res
	}
	tox := func(t *testing.T, svc *service.Service, id, code, op string, generation, ppb int64) service.BlindSampleResult {
		t.Helper()
		res, err := svc.SubmitBlindSample(ctx, service.BlindSampleRequest{
			TaskID: id, BlindCode: code, OperationKey: op, Generation: generation, ToxinPPB: &ppb,
		})
		if err != nil {
			t.Fatalf("toxin %s gen%d: %v", code, generation, err)
		}
		return res
	}
	rejudge := func(t *testing.T, svc *service.Service, id, op string, generation int64) {
		t.Helper()
		if _, err := svc.CreateRejudgement(ctx, service.RejudgementRequest{
			TaskID: id, OperationKey: op, Generation: generation, Kind: arbiter.AnomalyCutDivergence, Subjects: []string{"BC-1"},
		}); err != nil {
			t.Fatalf("rejudgement gen%d: %v", generation, err)
		}
	}
	chemistryAt := func(t *testing.T, svc *service.Service, id string, generation int64) {
		t.Helper()
		if _, err := svc.SubmitChemistry(ctx, service.ChemistryRequest{
			TaskID: id, OperationKey: "chem-g2", Generation: generation, MoisturePercent: 6500, PH: 560, FreeFattyAcidPercent: 100,
		}); err != nil {
			t.Fatalf("chemistry gen%d: %v", generation, err)
		}
	}
	reviewAt := func(t *testing.T, svc *service.Service, id, reviewer string, generation int64, decision evidence.Decision) {
		t.Helper()
		if _, err := svc.SubmitReview(ctx, service.ReviewRequest{
			TaskID: id, OperationKey: "rev-g2-" + reviewer, Generation: generation, ReviewerID: reviewer, Decision: decision,
		}); err != nil {
			t.Fatalf("review %s gen%d: %v", reviewer, generation, err)
		}
	}
	completeGenerationTwoAfterOldRejudge := func(t *testing.T, svc *service.Service) string {
		t.Helper()
		id := setupThroughCollection(t, svc)
		cut(t, svc, id, "BC-1", "cut-g1-BC-1", 1)
		rejudge(t, svc, id, "rej-g1", 1)
		cut(t, svc, id, "BC-1", "cut-g2-BC-1", 2)
		cut(t, svc, id, "BC-2", "cut-g2-BC-2", 2)
		tox(t, svc, id, "BC-1", "tox-g2-BC-1", 2, 5)
		tox(t, svc, id, "BC-2", "tox-g2-BC-2", 2, 5)
		chemistryAt(t, svc, id, 2)
		reviewAt(t, svc, id, "reviewer-c", 2, evidence.DecisionApprove)
		reviewAt(t, svc, id, "reviewer-d", 2, evidence.DecisionApprove)
		return id
	}

	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "old cut score does not complete current cut scoring",
			run: func(t *testing.T) {
				svc := newTestService(t, store.NewMemory())
				id := setupThroughCollection(t, svc)
				cut(t, svc, id, "BC-1", "cut-g1-BC-1", 1)
				rejudge(t, svc, id, "rej-g1", 1)

				res := cut(t, svc, id, "BC-2", "cut-g2-BC-2", 2)
				if res.Task.Generation != 2 {
					t.Fatalf("generation = %d, want 2", res.Task.Generation)
				}
				if res.Task.State != task.StateCutScoring {
					t.Fatalf("state = %q, want %q", res.Task.State, task.StateCutScoring)
				}
			},
		},
		{
			name: "old toxin reading does not open current reveal gate",
			run: func(t *testing.T) {
				svc := newTestService(t, store.NewMemory())
				id := toToxin(t, svc)
				tox(t, svc, id, "BC-1", "tox-g1-BC-1", 1, 5)
				rejudge(t, svc, id, "rej-g1", 1)

				res := tox(t, svc, id, "BC-2", "tox-g2-BC-2", 2, 5)
				if res.Task.State != task.StateToxinVerification {
					t.Fatalf("state = %q, want %q", res.Task.State, task.StateToxinVerification)
				}
				if res.Revealed {
					t.Fatal("old toxin evidence opened the current reveal gate")
				}
				audit, err := svc.Audit(ctx, id)
				if err != nil {
					t.Fatalf("audit: %v", err)
				}
				for _, sample := range audit.BlindSamples {
					if sample.Sealed || sample.RevealedBinID != "" || sample.RevealGeneration != 0 {
						t.Fatalf("blind sample %s revealed by mixed-generation toxin evidence: %+v", sample.BlindCode, sample)
					}
				}
			},
		},
		{
			name: "old rejudgement does not block ready to dry",
			run: func(t *testing.T) {
				svc := newTestService(t, store.NewMemory())
				id := completeGenerationTwoAfterOldRejudge(t, svc)

				res, err := svc.Finalize(ctx, service.FinalizeRequest{
					TaskID: id, OperationKey: "fin-ready-g2", Generation: 2, Decision: arbiter.DecisionReadyToDry,
				})
				if err != nil {
					t.Fatalf("ready-to-dry finalize: %v", err)
				}
				if res.Task.State != task.StateReadyToDry || res.Credential.Generation != 2 {
					t.Fatalf("final result state=%q credential_generation=%d", res.Task.State, res.Credential.Generation)
				}
			},
		},
		{
			name: "old rejudgement alone does not support risk isolation",
			run: func(t *testing.T) {
				svc := newTestService(t, store.NewMemory())
				id := completeGenerationTwoAfterOldRejudge(t, svc)

				_, err := svc.Finalize(ctx, service.FinalizeRequest{
					TaskID: id, OperationKey: "fin-risk-g2", Generation: 2, Decision: arbiter.DecisionRiskIsolated,
				})
				var ce *service.CodedError
				if !errors.As(err, &ce) || ce.Code != service.CodeInvalidDecision {
					t.Fatalf("expected invalid decision from old rejudgement, got %v", err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}
