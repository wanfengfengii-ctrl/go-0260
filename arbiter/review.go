package arbiter

import (
	"fmt"
	"sort"

	"cacaoferment/catalog"
	"cacaoferment/evidence"
)

// ReviewVerdict summarizes the independent-review state for a task generation.
type ReviewVerdict struct {
	ReviewerIDs []string
	Approvals   int
	Rejections  int
	Complete    bool
}

// AssessReviews validates that each review comes from a qualified reviewer who
// is not one of the boxing personnel, and reports whether at least two distinct
// reviewers have decided. Boxing and review personnel must never overlap, and
// the two reviewers must differ (rule 8).
func AssessReviews(reviews []evidence.ReviewRecord, boxers []string, snapshot catalog.RuleSnapshot) ReviewVerdict {
	boxerSet := make(map[string]struct{}, len(boxers))
	for _, b := range boxers {
		boxerSet[b] = struct{}{}
	}
	seen := make(map[string]evidence.Decision)
	for _, r := range reviews {
		if !snapshot.IsQualifiedReviewer(r.ReviewerID) {
			continue
		}
		if _, isBoxer := boxerSet[r.ReviewerID]; isBoxer {
			continue
		}
		if _, dup := seen[r.ReviewerID]; dup {
			continue
		}
		seen[r.ReviewerID] = r.Decision
	}
	v := ReviewVerdict{}
	for id, d := range seen {
		v.ReviewerIDs = append(v.ReviewerIDs, id)
		if d == evidence.DecisionApprove {
			v.Approvals++
		} else {
			v.Rejections++
		}
	}
	sort.Strings(v.ReviewerIDs)
	v.Complete = len(v.ReviewerIDs) >= 2
	return v
}

// ErrReviewsIncomplete reports that finalize was attempted before two distinct
// qualified reviewers had decided.
var ErrReviewsIncomplete = fmt.Errorf("arbiter: at least two distinct qualified reviewers required")
