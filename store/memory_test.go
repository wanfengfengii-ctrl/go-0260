package store

import (
	"context"
	"errors"
	"testing"

	"cacaoferment/lease"
)

func TestSaveLeaseConflictAcrossTasks(t *testing.T) {
	s := NewMemory()
	ctx := context.Background()
	l1 := lease.LeaseRecord{
		LeaseID:      "t1:bin:bin-1",
		TaskID:       "task-1",
		Generation:   1,
		ResourceType: lease.ResourceBin,
		ResourceID:   "bin-1",
		Status:       lease.LeaseAcquired,
	}
	if err := s.SaveLease(ctx, l1); err != nil {
		t.Fatalf("first lease: %v", err)
	}
	l2 := l1
	l2.LeaseID = "t2:bin:bin-1"
	l2.TaskID = "task-2"
	if err := s.SaveLease(ctx, l2); !errors.Is(err, ErrResourceOccupied) {
		t.Fatalf("expected ErrResourceOccupied, got %v", err)
	}
}

func TestListTasksSorted(t *testing.T) {
	s := NewMemory()
	ctx := context.Background()
	_ = s.SaveLease(ctx, lease.LeaseRecord{LeaseID: "a", TaskID: "z", ResourceType: lease.ResourceBin, ResourceID: "b", Status: lease.LeaseReleased})
	_ = s.SaveLease(ctx, lease.LeaseRecord{LeaseID: "b", TaskID: "z", ResourceType: lease.ResourceBin, ResourceID: "a", Status: lease.LeaseReleased})
	got, err := s.ListLeases(ctx, "z")
	if err != nil {
		t.Fatalf("ListLeases: %v", err)
	}
	if len(got) != 2 || got[0].ResourceID != "a" || got[1].ResourceID != "b" {
		t.Fatalf("expected sorted leases, got %+v", got)
	}
}
