package eval

import (
	"path/filepath"
	"testing"
)

func TestWriteAndLoadResultsJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.json")
	want := testSummary()
	if err := WriteResultsJSON("L1+L2 (real ONNX embedder)", want, want, path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadResultsJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != "L1+L2 (real ONNX embedder)" || got.HeldOut.N != want.N ||
		got.HeldOut.CheapModelRecall != want.CheapModelRecall {
		t.Fatalf("round-tripped = %+v, want mode/N/recall matching %+v", got, want)
	}
}

func TestLoadResultsJSONMissingFile(t *testing.T) {
	if _, err := LoadResultsJSON("/no/such/results.json"); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func TestCheckDriftNoDrift(t *testing.T) {
	s := testSummary()
	baseline := ResultSet{HeldOut: s}
	fresh := ResultSet{HeldOut: s} // identical -> no drift
	if problems := CheckDrift(baseline, fresh); len(problems) != 0 {
		t.Fatalf("expected no drift for identical summaries, got %v", problems)
	}
}

func TestCheckDriftDetectsCostRegression(t *testing.T) {
	baseline := ResultSet{HeldOut: Summary{N: 100, RoutedCost: 1.0, BaselineCost: 2.0, RoutedScoreSum: 90, BaselineScoreSum: 95, CheapModelRecall: 0.8}}
	// Same accuracy/recall, but routing got much more expensive — savings
	// dropped from 50% to 0%, well beyond the 10-point tolerance.
	fresh := ResultSet{HeldOut: Summary{N: 100, RoutedCost: 2.0, BaselineCost: 2.0, RoutedScoreSum: 90, BaselineScoreSum: 95, CheapModelRecall: 0.8}}

	problems := CheckDrift(baseline, fresh)
	if len(problems) != 1 {
		t.Fatalf("expected exactly 1 drifted metric (cost savings), got %v", problems)
	}
}

func TestCheckDriftWithinTolerance(t *testing.T) {
	baseline := ResultSet{HeldOut: Summary{N: 100, RoutedCost: 1.0, BaselineCost: 2.0, RoutedScoreSum: 90, BaselineScoreSum: 100, CheapModelRecall: 0.80}}
	// accuracy 90.0 -> 91.0 (1pt, within ±5), recall 80% -> 81% (1pt, within
	// ±5), cost savings 50% -> 51% (within ±10): no problems expected.
	fresh := ResultSet{HeldOut: Summary{N: 100, RoutedCost: 0.98, BaselineCost: 2.0, RoutedScoreSum: 91, BaselineScoreSum: 100, CheapModelRecall: 0.81}}
	if problems := CheckDrift(baseline, fresh); len(problems) != 0 {
		t.Fatalf("expected no drift within tolerance, got %v", problems)
	}
}
