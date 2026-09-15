package eval

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
)

// ResultSet is the machine-readable form of one eval run — the same numbers
// RESULTS.md renders for humans, as JSON for CI to diff against a committed
// baseline (see CheckDrift).
type ResultSet struct {
	Mode    string  `json:"mode"`
	Train   Summary `json:"train"`
	HeldOut Summary `json:"held_out"`
}

// WriteResultsJSON writes a ResultSet to path.
func WriteResultsJSON(mode string, train, heldOut Summary, path string) error {
	b, err := json.MarshalIndent(ResultSet{Mode: mode, Train: train, HeldOut: heldOut}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// LoadResultsJSON reads a ResultSet previously written by WriteResultsJSON.
func LoadResultsJSON(path string) (ResultSet, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return ResultSet{}, err
	}
	var rs ResultSet
	if err := json.Unmarshal(b, &rs); err != nil {
		return ResultSet{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return rs, nil
}

// Drift tolerances for CheckDrift, in percentage points. The router's
// decisions are deterministic (nearest-centroid, no randomness), so a
// same-dataset re-run should reproduce baseline numbers almost exactly —
// these exist to catch a real regression in router behavior, not to absorb
// run-to-run noise.
const (
	CostSavingsTolerancePct = 10.0
	AccuracyTolerancePct    = 5.0
	RecallTolerancePct      = 5.0
)

// CheckDrift compares fresh's held-out numbers against baseline's committed
// held-out numbers and reports any that moved by more than the fixed
// tolerance above — the CI regression gate for `.github/workflows/eval.yml`.
// An empty return means no drift.
func CheckDrift(baseline, fresh ResultSet) []string {
	var problems []string
	check := func(name string, base, cur, tolerance float64) {
		if diff := math.Abs(cur - base); diff > tolerance {
			problems = append(problems, fmt.Sprintf(
				"%s drifted: baseline %.1f, now %.1f (tolerance ±%.1f)", name, base, cur, tolerance))
		}
	}
	check("held-out cost savings %", baseline.HeldOut.CostSavingsPct(), fresh.HeldOut.CostSavingsPct(), CostSavingsTolerancePct)
	check("held-out accuracy %", baseline.HeldOut.Accuracy()*100, fresh.HeldOut.Accuracy()*100, AccuracyTolerancePct)
	check("held-out cheap-model recall %", baseline.HeldOut.CheapModelRecall*100, fresh.HeldOut.CheapModelRecall*100, RecallTolerancePct)
	return problems
}
