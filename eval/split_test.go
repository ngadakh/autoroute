package eval

import (
	"fmt"
	"testing"
)

func makeRows(n int) []Row {
	var rows []Row
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("bench-%02d", i)
		rows = append(rows,
			Row{SampleID: fmt.Sprintf("s%d-a", i), EvalName: name, Score: map[string]float64{}, Cost: map[string]float64{}},
			Row{SampleID: fmt.Sprintf("s%d-b", i), EvalName: name, Score: map[string]float64{}, Cost: map[string]float64{}},
		)
	}
	return rows
}

func TestSplitDeterministic(t *testing.T) {
	rows := makeRows(25)
	train1, heldOut1 := Split(rows)
	train2, heldOut2 := Split(rows)
	if len(train1) != len(train2) || len(heldOut1) != len(heldOut2) {
		t.Fatalf("split isn't stable: (%d,%d) vs (%d,%d)", len(train1), len(heldOut1), len(train2), len(heldOut2))
	}
}

func TestSplitRoughlyTwentyPercentByCategory(t *testing.T) {
	rows := makeRows(25) // 25 categories, every 5th held out -> 5
	_, heldOut := Split(rows)
	names := map[string]bool{}
	for _, r := range heldOut {
		names[r.EvalName] = true
	}
	if len(names) != 5 {
		t.Fatalf("held-out categories = %d, want 5", len(names))
	}
}

func TestSplitNoOverlapBetweenTrainAndHeldOut(t *testing.T) {
	rows := makeRows(10)
	train, heldOut := Split(rows)
	if len(train)+len(heldOut) != len(rows) {
		t.Fatalf("train+heldOut = %d, want %d (no rows dropped)", len(train)+len(heldOut), len(rows))
	}
	trainNames := map[string]bool{}
	for _, r := range train {
		trainNames[r.EvalName] = true
	}
	for _, r := range heldOut {
		if trainNames[r.EvalName] {
			t.Fatalf("eval_name %q appears in both train and held-out", r.EvalName)
		}
	}
}
