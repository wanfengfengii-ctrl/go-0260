package service_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"cacaoferment/service"
	"cacaoferment/store"
	"cacaoferment/task"
)

func TestModel_SQLiteRejectedTurnBatchRollsBackAndReleasesConnection(t *testing.T) {
	const timeout = time.Second

	cases := []struct {
		name          string
		afterRejected func(t *testing.T, svc *service.Service, taskID string)
	}{
		{
			name: "subsequent audit sees no partial collection writes",
			afterRejected: func(t *testing.T, svc *service.Service, taskID string) {
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()

				audit, err := svc.Audit(ctx, taskID)
				if err != nil {
					t.Fatalf("audit after rejected turn batch: %v", err)
				}
				if audit.Task.State != task.StateTurnCollection {
					t.Fatalf("state after rejected turn batch = %q, want %q", audit.Task.State, task.StateTurnCollection)
				}
				if len(audit.CoverageCells) != 0 {
					t.Fatalf("rejected turn batch left %d coverage cells: %+v", len(audit.CoverageCells), audit.CoverageCells)
				}
				if len(audit.Evidence) != 0 {
					t.Fatalf("rejected turn batch left %d evidence records: %+v", len(audit.Evidence), audit.Evidence)
				}
				for _, op := range audit.Operations {
					if op.OperationKey == "bad-turns" {
						t.Fatalf("rejected turn batch recorded operation: %+v", op)
					}
				}
			},
		},
		{
			name: "subsequent valid collection can commit",
			afterRejected: func(t *testing.T, svc *service.Service, taskID string) {
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()

				res, err := svc.SubmitTurnReadings(ctx, service.TurnReadingsRequest{
					TaskID: taskID, OperationKey: "good-turns", Generation: 1,
					Readings: []service.TurnReading{
						{BinID: "bin-1", TurnNode: 1, TemperatureCentiC: 4000, DurationMinutes: 60, TurnCount: 1},
						{BinID: "bin-1", TurnNode: 2, TemperatureCentiC: 4300, DurationMinutes: 120, TurnCount: 2},
						{BinID: "bin-1", TurnNode: 3, TemperatureCentiC: 4600, DurationMinutes: 180, TurnCount: 3},
					},
				})
				if err != nil {
					t.Fatalf("valid collection after rejected turn batch: %v", err)
				}
				if !res.Complete || len(res.Cells) != 3 || res.Task.State != task.StateCutScoring {
					t.Fatalf("valid collection result = complete:%v cells:%d state:%q, want complete:true cells:3 state:%q",
						res.Complete, len(res.Cells), res.Task.State, task.StateCutScoring)
				}
			},
		},
		{
			name: "subsequent lock returns stable business error",
			afterRejected: func(t *testing.T, svc *service.Service, taskID string) {
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()

				_, err := svc.Lock(ctx, defaultLock())
				var ce *service.CodedError
				if !errors.As(err, &ce) || ce.Code != service.CodeResourceOccupied {
					t.Fatalf("lock after rejected turn batch error = %v, want code %q", err, service.CodeResourceOccupied)
				}

				auditCtx, auditCancel := context.WithTimeout(context.Background(), timeout)
				defer auditCancel()
				if _, err := svc.Audit(auditCtx, taskID); err != nil {
					t.Fatalf("audit after failed follow-up lock: %v", err)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			st, err := store.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "wal"))
			if err != nil {
				t.Fatalf("open sqlite: %v", err)
			}
			svc := newTestService(t, st)
			taskID := toCollection(t, svc)

			_, err = svc.SubmitTurnReadings(context.Background(), service.TurnReadingsRequest{
				TaskID: taskID, OperationKey: "bad-turns", Generation: 1,
				Readings: []service.TurnReading{
					{BinID: "bin-1", TurnNode: 1, TemperatureCentiC: 4000, DurationMinutes: 60, TurnCount: 1},
					{BinID: "bin-1", TurnNode: 2, TemperatureCentiC: 5700, DurationMinutes: 120, TurnCount: 2},
				},
			})
			var ce *service.CodedError
			if !errors.As(err, &ce) || ce.Code != service.CodeInvalidReading {
				t.Fatalf("rejected turn batch error = %v, want code %q", err, service.CodeInvalidReading)
			}

			tc.afterRejected(t, svc, taskID)
		})
	}
}
