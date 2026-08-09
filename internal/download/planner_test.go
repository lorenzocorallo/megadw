package download

import (
	"errors"
	"testing"
)

func TestSegmentPlannerBoundaryCases(t *testing.T) {
	const segmentSize = int64(8)
	tests := []struct {
		name  string
		size  int64
		count int64
	}{
		{name: "zero", size: 0, count: 0},
		{name: "one byte", size: 1, count: 1},
		{name: "exact", size: segmentSize, count: 1},
		{name: "plus one", size: segmentSize + 1, count: 2},
		{name: "multi", size: segmentSize*3 + 2, count: 4},
		{name: "multi tebibyte", size: 4 << 40, count: (4 << 40) / segmentSize},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			planner, err := NewSegmentPlanner(test.size, segmentSize)
			if err != nil {
				t.Fatal(err)
			}
			if planner.Count != test.count {
				t.Fatalf("count = %d, want %d", planner.Count, test.count)
			}
			if test.count > 0 {
				first, err := planner.Segment(0)
				wantFirstEnd := segmentSize - 1
				if test.size < wantFirstEnd+1 {
					wantFirstEnd = test.size - 1
				}
				if err != nil || first.Start != 0 || first.End != wantFirstEnd {
					t.Fatalf("first = %#v, error = %v", first, err)
				}
				last, err := planner.Segment(test.count - 1)
				if err != nil || last.End != test.size-1 {
					t.Fatalf("last = %#v, error = %v", last, err)
				}
			}
		})
	}
}

func TestBitmapSerializationAndValidation(t *testing.T) {
	bitmap, err := NewBitmap(17)
	if err != nil {
		t.Fatal(err)
	}
	for _, index := range []int64{0, 8, 16} {
		if err := bitmap.Set(index); err != nil {
			t.Fatal(err)
		}
	}
	if bitmap.Count() != 3 || !bitmap.IsSet(8) {
		t.Fatalf("bitmap = %08b, count = %d", []byte(bitmap), bitmap.Count())
	}
	clone := bitmap.Clone()
	if err := clone.Clear(8); err != nil {
		t.Fatal(err)
	}
	if !bitmap.IsSet(8) || clone.IsSet(8) {
		t.Fatal("bitmap clone aliases original")
	}
	if err := bitmap.Validate(17); err != nil {
		t.Fatal(err)
	}
	bitmap[len(bitmap)-1] |= 0x80
	if err := bitmap.Validate(17); !errors.Is(err, ErrInvalidSegmentPlan) {
		t.Fatalf("invalid high bit error = %v", err)
	}
}

func TestStateTransitionsRejectTerminalRegressions(t *testing.T) {
	if err := TransitionJobState(JobDownloading, JobPausedRecovery); err != nil {
		t.Fatal(err)
	}
	if err := TransitionJobState(JobCompleted, JobDownloading); !errors.Is(err, ErrInvalidStateTransition) {
		t.Fatalf("completed regression error = %v", err)
	}
	if err := TransitionFileState(FileVerifying, FileMoving); err != nil {
		t.Fatal(err)
	}
}
