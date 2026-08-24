package service_test

import (
	"context"
	"errors"
	"testing"

	"cacaoferment/service"
	"cacaoferment/store"
	"cacaoferment/task"
)

func TestModel_TurnCoverageClosesPerLockedBin(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "two locked bins require every bin node before cut scoring",
			run: func(t *testing.T) {
				st := store.NewMemory()
				svc := newTestService(t, st)
				id := toCollection(t, svc)

				res, err := svc.SubmitTurnReadings(ctx, service.TurnReadingsRequest{
					TaskID: id, OperationKey: "bin1-all", Generation: 1,
					Readings: []service.TurnReading{
						{BinID: "bin-1", TurnNode: 1, TemperatureCentiC: 4000, DurationMinutes: 60, TurnCount: 1},
						{BinID: "bin-1", TurnNode: 2, TemperatureCentiC: 4300, DurationMinutes: 120, TurnCount: 2},
						{BinID: "bin-1", TurnNode: 3, TemperatureCentiC: 4600, DurationMinutes: 180, TurnCount: 3},
					},
				})
				if err != nil {
					t.Fatalf("bin-1 readings: %v", err)
				}
				if res.Complete {
					t.Fatal("bin-1-only coverage reported complete")
				}
				if res.Task.State != task.StateTurnCollection {
					t.Fatalf("state after bin-1-only coverage = %q, want %q", res.Task.State, task.StateTurnCollection)
				}
				cells, err := st.ListCoverageCells(ctx, id)
				if err != nil {
					t.Fatalf("list cells after bin-1: %v", err)
				}
				if len(cells) != 3 {
					t.Fatalf("cells after bin-1 = %d, want 3", len(cells))
				}
				for _, cell := range cells {
					if cell.BinID != "bin-1" {
						t.Fatalf("unexpected cell after bin-1-only coverage: %+v", cell)
					}
				}

				res, err = svc.SubmitTurnReadings(ctx, service.TurnReadingsRequest{
					TaskID: id, OperationKey: "bin2-node1", Generation: 1,
					Readings: []service.TurnReading{
						{BinID: "bin-2", TurnNode: 1, TemperatureCentiC: 4050, DurationMinutes: 60, TurnCount: 1},
					},
				})
				if err != nil {
					t.Fatalf("bin-2 same turn node should be accepted: %v", err)
				}
				if res.Complete {
					t.Fatal("coverage reported complete before bin-2 covered all nodes")
				}
				if res.Task.State != task.StateTurnCollection {
					t.Fatalf("state after bin-2 node 1 = %q, want %q", res.Task.State, task.StateTurnCollection)
				}
				cells, err = st.ListCoverageCells(ctx, id)
				if err != nil {
					t.Fatalf("list cells after bin-2 node 1: %v", err)
				}
				if len(cells) != 4 {
					t.Fatalf("cells after bin-2 node 1 = %d, want 4", len(cells))
				}
				foundBin2Node1 := false
				for _, cell := range cells {
					if cell.BinID == "bin-2" && cell.TurnNode == 1 {
						foundBin2Node1 = true
					}
				}
				if !foundBin2Node1 {
					t.Fatal("bin-2 turn node 1 was not persisted")
				}

				_, err = svc.SubmitTurnReadings(ctx, service.TurnReadingsRequest{
					TaskID: id, OperationKey: "bin2-node1-duplicate", Generation: 1,
					Readings: []service.TurnReading{
						{BinID: "bin-2", TurnNode: 1, TemperatureCentiC: 4100, DurationMinutes: 60, TurnCount: 1},
					},
				})
				var ce *service.CodedError
				if !errors.As(err, &ce) || ce.Code != service.CodeInvalidReading {
					t.Fatalf("duplicate bin-2 node 1 error = %v, want invalid reading", err)
				}
				cells, err = st.ListCoverageCells(ctx, id)
				if err != nil {
					t.Fatalf("list cells after duplicate: %v", err)
				}
				if len(cells) != 4 {
					t.Fatalf("duplicate changed cell count to %d, want 4", len(cells))
				}

				res, err = svc.SubmitTurnReadings(ctx, service.TurnReadingsRequest{
					TaskID: id, OperationKey: "bin2-rest", Generation: 1,
					Readings: []service.TurnReading{
						{BinID: "bin-2", TurnNode: 2, TemperatureCentiC: 4350, DurationMinutes: 120, TurnCount: 2},
						{BinID: "bin-2", TurnNode: 3, TemperatureCentiC: 4650, DurationMinutes: 180, TurnCount: 3},
					},
				})
				if err != nil {
					t.Fatalf("remaining bin-2 readings: %v", err)
				}
				if !res.Complete {
					t.Fatal("full bin x node coverage was not complete")
				}
				if res.Task.State != task.StateCutScoring {
					t.Fatalf("state after all locked cells = %q, want %q", res.Task.State, task.StateCutScoring)
				}
			},
		},
		{
			name: "invalid batch leaves no partial cells or evidence",
			run: func(t *testing.T) {
				st := store.NewMemory()
				svc := newTestService(t, st)
				id := toCollection(t, svc)

				_, err := svc.SubmitTurnReadings(ctx, service.TurnReadingsRequest{
					TaskID: id, OperationKey: "bad-batch", Generation: 1,
					Readings: []service.TurnReading{
						{BinID: "bin-1", TurnNode: 1, TemperatureCentiC: 4000, DurationMinutes: 60, TurnCount: 1},
						{BinID: "bin-1", TurnNode: 2, TemperatureCentiC: 9000, DurationMinutes: 120, TurnCount: 2},
					},
				})
				var ce *service.CodedError
				if !errors.As(err, &ce) || ce.Code != service.CodeInvalidReading {
					t.Fatalf("bad batch error = %v, want invalid reading", err)
				}
				cells, err := st.ListCoverageCells(ctx, id)
				if err != nil {
					t.Fatalf("list cells after bad batch: %v", err)
				}
				if len(cells) != 0 {
					t.Fatalf("bad batch left %d coverage cells, want 0", len(cells))
				}
				versions, err := st.ListEvidence(ctx, id)
				if err != nil {
					t.Fatalf("list evidence after bad batch: %v", err)
				}
				if len(versions) != 0 {
					t.Fatalf("bad batch left %d evidence versions, want 0", len(versions))
				}
				view, err := svc.Get(ctx, id)
				if err != nil {
					t.Fatalf("get after bad batch: %v", err)
				}
				if view.Task.State != task.StateTurnCollection {
					t.Fatalf("state after bad batch = %q, want %q", view.Task.State, task.StateTurnCollection)
				}
			},
		},
		{
			name: "single locked bin full coverage still completes",
			run: func(t *testing.T) {
				st := store.NewMemory()
				svc := newTestService(t, st)
				req := defaultLock()
				req.BinIDs = []string{"bin-1"}
				req.BlindSamples = []service.BlindSampleSpec{
					{BlindCode: "BC-1", SampleSize: 100, BinID: "bin-1"},
				}
				locked, err := svc.Lock(ctx, req)
				if err != nil {
					t.Fatalf("single-bin lock: %v", err)
				}
				id := locked.Task.TaskID
				box(t, svc, id, "reviewer-a")
				box(t, svc, id, "reviewer-b")
				startEquipment(t, svc, id)

				res, err := svc.SubmitTurnReadings(ctx, service.TurnReadingsRequest{
					TaskID: id, OperationKey: "single-bin-turns", Generation: 1,
					Readings: []service.TurnReading{
						{BinID: "bin-1", TurnNode: 1, TemperatureCentiC: 4000, DurationMinutes: 60, TurnCount: 1},
						{BinID: "bin-1", TurnNode: 2, TemperatureCentiC: 4300, DurationMinutes: 120, TurnCount: 2},
						{BinID: "bin-1", TurnNode: 3, TemperatureCentiC: 4600, DurationMinutes: 180, TurnCount: 3},
					},
				})
				if err != nil {
					t.Fatalf("single-bin readings: %v", err)
				}
				if !res.Complete {
					t.Fatal("single-bin full coverage was not complete")
				}
				if res.Task.State != task.StateCutScoring {
					t.Fatalf("single-bin state = %q, want %q", res.Task.State, task.StateCutScoring)
				}
				cells, err := st.ListCoverageCells(ctx, id)
				if err != nil {
					t.Fatalf("list single-bin cells: %v", err)
				}
				if len(cells) != 3 {
					t.Fatalf("single-bin cell count = %d, want 3", len(cells))
				}
				for _, cell := range cells {
					if cell.BinID != "bin-1" {
						t.Fatalf("unexpected single-bin cell: %+v", cell)
					}
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, tc.run)
	}
}
