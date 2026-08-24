package service_test

import (
	"context"
	"errors"
	"testing"

	"cacaoferment/arbiter"
	"cacaoferment/service"
	"cacaoferment/store"
	"cacaoferment/task"
)

func toCollection(t *testing.T, svc *service.Service) string {
	t.Helper()
	id := mustLock(t, svc)
	box(t, svc, id, "reviewer-a")
	box(t, svc, id, "reviewer-b")
	startEquipment(t, svc, id)
	return id
}

func TestTurnCoverageCompleteAdvances(t *testing.T) {
	svc := newTestService(t, store.NewMemory())
	id := toCollection(t, svc)
	collect(t, svc, id)
	v, _ := svc.Get(context.Background(), id)
	if v.Task.State != task.StateCutScoring {
		t.Fatalf("expected cut_scoring after complete coverage, got %q", v.Task.State)
	}
}

func TestTurnDuplicateRejected(t *testing.T) {
	svc := newTestService(t, store.NewMemory())
	id := toCollection(t, svc)
	readings := []service.TurnReading{{BinID: "bin-1", TurnNode: 1, TemperatureCentiC: 4000, DurationMinutes: 60, TurnCount: 1}}
	if _, err := svc.SubmitTurnReadings(context.Background(), service.TurnReadingsRequest{
		TaskID: id, OperationKey: "t1", Generation: 1, Readings: readings,
	}); err != nil {
		t.Fatalf("first reading: %v", err)
	}
	_, err := svc.SubmitTurnReadings(context.Background(), service.TurnReadingsRequest{
		TaskID: id, OperationKey: "t2", Generation: 1, Readings: readings,
	})
	var ce *service.CodedError
	if !errors.As(err, &ce) || ce.Code != service.CodeInvalidReading {
		t.Fatalf("expected invalid reading for duplicate node, got %v", err)
	}
}

func TestTurnGapFillFirstNodeRejected(t *testing.T) {
	svc := newTestService(t, store.NewMemory())
	id := toCollection(t, svc)
	_, err := svc.SubmitTurnReadings(context.Background(), service.TurnReadingsRequest{
		TaskID: id, OperationKey: "t1", Generation: 1,
		Readings: []service.TurnReading{{BinID: "bin-1", TurnNode: 1, TemperatureCentiC: 4000, DurationMinutes: 60, TurnCount: 1, GapFillFlag: true}},
	})
	var ce *service.CodedError
	if !errors.As(err, &ce) || ce.Code != service.CodeInvalidReading {
		t.Fatalf("expected invalid reading for gap fill on first node, got %v", err)
	}
}

func TestTurnSlopeBoundary(t *testing.T) {
	// Max slope is 400 milli-deg/min. With duration 60 and ambient 3000 centiC,
	// temp 5400 gives exactly 400 (pass); temp 5406 gives 401 (break).
	t.Run("exactly at maximum", func(t *testing.T) {
		svc := newTestService(t, store.NewMemory())
		id := toCollection(t, svc)
		res, err := svc.SubmitTurnReadings(context.Background(), service.TurnReadingsRequest{
			TaskID: id, OperationKey: "t1", Generation: 1,
			Readings: []service.TurnReading{{BinID: "bin-1", TurnNode: 1, TemperatureCentiC: 5400, DurationMinutes: 60, TurnCount: 1}},
		})
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		if len(res.Anomalies) != 0 {
			t.Fatalf("expected no anomaly at exact boundary, got %v", res.Anomalies)
		}
		if res.Cells[0].SlopeMilliPerMin != 400 {
			t.Fatalf("expected slope 400, got %d", res.Cells[0].SlopeMilliPerMin)
		}
	})
	t.Run("one unit past maximum", func(t *testing.T) {
		svc := newTestService(t, store.NewMemory())
		id := toCollection(t, svc)
		res, err := svc.SubmitTurnReadings(context.Background(), service.TurnReadingsRequest{
			TaskID: id, OperationKey: "t1", Generation: 1,
			Readings: []service.TurnReading{{BinID: "bin-1", TurnNode: 1, TemperatureCentiC: 5406, DurationMinutes: 60, TurnCount: 1}},
		})
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		if res.Cells[0].SlopeMilliPerMin != 401 {
			t.Fatalf("expected slope 401, got %d", res.Cells[0].SlopeMilliPerMin)
		}
		if !containsKind(res.Anomalies, arbiter.AnomalyTemperatureBreak) {
			t.Fatalf("expected temperature break anomaly, got %v", res.Anomalies)
		}
	})
}

func containsKind(list []arbiter.AnomalyKind, k arbiter.AnomalyKind) bool {
	for _, a := range list {
		if a == k {
			return true
		}
	}
	return false
}
