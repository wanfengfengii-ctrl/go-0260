package arbiter

import (
	"testing"

	"cacaoferment/catalog"
	"cacaoferment/evidence"
	"cacaoferment/task"
)

func TestTerminalDecisionState(t *testing.T) {
	if DecisionReadyToDry.State() != task.StateReadyToDry {
		t.Fatal("ready-to-dry mapping wrong")
	}
	if DecisionRiskIsolated.State() != task.StateRiskIsolated {
		t.Fatal("risk-isolated mapping wrong")
	}
	if DecisionCancelled.State() != task.StateCancelled {
		t.Fatal("cancelled mapping wrong")
	}
	if !DecisionReadyToDry.Valid() {
		t.Fatal("ready-to-dry should be valid")
	}
	if (TerminalDecision("bogus")).Valid() {
		t.Fatal("bogus decision should be invalid")
	}
}

func TestAssessReviews(t *testing.T) {
	snap := catalog.RuleSnapshot{
		QualifiedReviewers: []catalog.Reviewer{
			{ID: "r-a", Qualified: true},
			{ID: "r-b", Qualified: true},
			{ID: "r-c", Qualified: true},
		},
	}
	reviews := []evidence.ReviewRecord{
		{ReviewerID: "r-a", Decision: evidence.DecisionApprove},
		{ReviewerID: "r-b", Decision: evidence.DecisionApprove},
	}
	v := AssessReviews(reviews, nil, snap)
	if !v.Complete || v.Approvals != 2 {
		t.Fatalf("verdict = %+v", v)
	}

	// A boxer may not review.
	v = AssessReviews(reviews, []string{"r-a"}, snap)
	if v.Complete {
		t.Fatal("boxer must not count as a reviewer")
	}

	// Only one distinct reviewer is incomplete.
	v = AssessReviews(reviews[:1], nil, snap)
	if v.Complete {
		t.Fatal("single reviewer must be incomplete")
	}
}

func TestCredentialDigestDeterministic(t *testing.T) {
	a := CredentialDigest("t1", 1, task.StateReadyToDry, "op", 42)
	b := CredentialDigest("t1", 1, task.StateReadyToDry, "op", 42)
	c := CredentialDigest("t1", 1, task.StateReadyToDry, "op", 43)
	if a != b {
		t.Fatal("digest must be deterministic")
	}
	if a == c {
		t.Fatal("digest must differ on tick")
	}
}
