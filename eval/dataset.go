// Package eval is the M4 eval harness: it replays a benchmark dataset
// through the exact same router pipeline the proxy uses in production
// (internal/router), and reports cost and accuracy against an always-frontier
// baseline. See docs/ARCHITECTURE.md's "What gets measured" section for
// the dataset this was built against — RouterBench
// (huggingface.co/datasets/withmartian/routerbench), converted to CSV by
// scripts/convert-routerbench.py (see scripts/setup-routerbench.sh).
package eval

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Row is one benchmark prompt: which benchmark it's from, and — for every
// model RouterBench scored — whether that model got it right and what it
// cost. AutoRoute only routes to three of these models (see Tiers in
// harness.go); the rest stay available for oracle-style comparisons.
type Row struct {
	SampleID string
	Prompt   string
	EvalName string
	Oracle   string
	Score    map[string]float64 // model name -> [0,1], usually 0/1
	Cost     map[string]float64 // model name -> USD for this one response
}

const (
	scorePrefix = "score:"
	costPrefix  = "cost:"
)

// LoadCSV reads a dataset produced by scripts/convert-routerbench.py. Column
// order isn't assumed beyond the fixed leading fields — score:/cost:
// columns are discovered from the header, so the model list only needs to
// agree between the Python converter and whatever's actually in the file.
func LoadCSV(path string) ([]Row, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	idx := make(map[string]int, len(header))
	for i, h := range header {
		idx[h] = i
	}
	required := []string{"sample_id", "prompt", "eval_name", "oracle_model_to_route_to"}
	for _, col := range required {
		if _, ok := idx[col]; !ok {
			return nil, fmt.Errorf("missing required column %q", col)
		}
	}

	var scoreCols, costCols []string
	for _, h := range header {
		switch {
		case strings.HasPrefix(h, scorePrefix):
			scoreCols = append(scoreCols, h)
		case strings.HasPrefix(h, costPrefix):
			costCols = append(costCols, h)
		}
	}
	if len(scoreCols) == 0 {
		return nil, fmt.Errorf("no score: columns found in header")
	}

	var rows []Row
	for lineNum := 2; ; lineNum++ {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNum, err)
		}

		row := Row{
			SampleID: rec[idx["sample_id"]],
			Prompt:   rec[idx["prompt"]],
			EvalName: rec[idx["eval_name"]],
			Oracle:   rec[idx["oracle_model_to_route_to"]],
			Score:    make(map[string]float64, len(scoreCols)),
			Cost:     make(map[string]float64, len(costCols)),
		}
		for _, h := range scoreCols {
			v, err := strconv.ParseFloat(rec[idx[h]], 64)
			if err != nil {
				return nil, fmt.Errorf("line %d: parse %s: %w", lineNum, h, err)
			}
			row.Score[strings.TrimPrefix(h, scorePrefix)] = v
		}
		for _, h := range costCols {
			v, err := strconv.ParseFloat(rec[idx[h]], 64)
			if err != nil {
				return nil, fmt.Errorf("line %d: parse %s: %w", lineNum, h, err)
			}
			row.Cost[strings.TrimPrefix(h, costPrefix)] = v
		}
		rows = append(rows, row)
	}
	return rows, nil
}
