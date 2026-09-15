// Command eval is the M4 eval harness: it replays RouterBench through
// AutoRoute's real router pipeline (internal/router, exercised exactly the
// way internal/proxy does) and reports cost and accuracy against an
// always-frontier baseline. See eval/, scripts/setup-routerbench.sh and
// `make eval-setup && make eval`.
//
// Like cmd/spike-embed, this always builds with cgo — CGO_ENABLED=0 (or
// missing model assets) still runs, but only exercises L1 heuristics,
// degrading exactly the way the production proxy does; the results clearly
// say which mode produced them.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ngadakh/autoroute/eval"
	"github.com/ngadakh/autoroute/internal/config"
	"github.com/ngadakh/autoroute/internal/router"
)

func main() {
	data := flag.String("data", "eval/data/routerbench_0shot.csv", "converted RouterBench CSV (see scripts/setup-routerbench.sh)")
	out := flag.String("out", "eval/RESULTS.md", "where to write the results markdown")
	chart := flag.String("chart", "eval/RESULTS_chart.svg", "where to write the cost/accuracy chart")
	jsonOut := flag.String("json", "eval/results.json", "where to write the machine-readable results")
	checkAgainst := flag.String("check-against", "", "compare this run's held-out numbers against a committed results.json and fail on drift (empty skips the check — see eval.CheckDrift)")
	lib := flag.String("lib", "third_party/onnxruntime/lib/libonnxruntime.dylib", "path to the ONNX Runtime shared library")
	model := flag.String("model", "models/all-MiniLM-L6-v2/model.onnx", "path to the ONNX model")
	vocab := flag.String("vocab", "models/all-MiniLM-L6-v2/vocab.txt", "path to vocab.txt")
	maxSeq := flag.Int("maxseq", 256, "maximum sequence length")
	thetaLow := flag.Float64("theta-low", 0.35, "L2 confidence low threshold (matches configs/catalogue.yaml)")
	thetaHigh := flag.Float64("theta-high", 0.70, "L2 confidence high threshold (matches configs/catalogue.yaml)")
	defaultTier := flag.String("default-tier", "frontier", "conservative default tier (matches configs/catalogue.yaml)")
	flag.Parse()

	if err := run(*data, *out, *chart, *jsonOut, *checkAgainst, *lib, *model, *vocab, *maxSeq, *thetaLow, *thetaHigh, *defaultTier); err != nil {
		fmt.Fprintf(os.Stderr, "eval failed: %v\n", err)
		os.Exit(1)
	}
}

func run(data, out, chart, jsonOut, checkAgainst, lib, model, vocab string, maxSeq int, thetaLow, thetaHigh float64, defaultTier string) error {
	clf, embedder, err := router.BuildClassifier(config.EmbeddingConfig{
		ModelPath:         model,
		VocabPath:         vocab,
		MaxSeqLen:         maxSeq,
		SharedLibraryPath: lib,
	})
	mode := "L1+L2 (real ONNX embedder)"
	if err != nil {
		fmt.Fprintf(os.Stderr, "L2 embedding classifier unavailable, running L1-only: %v\n", err)
		mode = "L1-only (degraded — no ONNX embedder; run `make setup` for the full L1+L2 eval)"
	}

	pipeline := router.NewPipeline(clf, embedder, thetaLow, thetaHigh, router.Tier(defaultTier))

	rows, err := eval.LoadCSV(data)
	if err != nil {
		return fmt.Errorf("load dataset (run `make eval-setup` first?): %w", err)
	}
	fmt.Fprintf(os.Stderr, "loaded %d rows from %s\n", len(rows), data)

	train, heldOut := eval.Split(rows)
	fmt.Fprintf(os.Stderr, "split: %d train, %d held-out\n", len(train), len(heldOut))

	h := &eval.Harness{Pipeline: pipeline}
	trainSummary := h.Run(train)
	heldOutSummary := h.Run(heldOut)

	if err := eval.WriteResultsMarkdown(mode, trainSummary, heldOutSummary, out); err != nil {
		return fmt.Errorf("write results: %w", err)
	}
	if err := eval.WriteChartSVG(trainSummary, heldOutSummary, chart); err != nil {
		return fmt.Errorf("write chart: %w", err)
	}
	if err := eval.WriteResultsJSON(mode, trainSummary, heldOutSummary, jsonOut); err != nil {
		return fmt.Errorf("write json: %w", err)
	}

	fmt.Printf("mode: %s\n\n", mode)
	fmt.Printf("%-10s %6s %12s %12s %8s %10s %10s %8s\n",
		"split", "n", "routed$", "baseline$", "save%", "acc", "base-acc", "recall")
	printSummary("train", trainSummary)
	printSummary("held-out", heldOutSummary)
	fmt.Printf("\nwrote %s, %s and %s\n", out, chart, jsonOut)

	if checkAgainst == "" {
		return nil
	}
	baseline, err := eval.LoadResultsJSON(checkAgainst)
	if err != nil {
		return fmt.Errorf("load baseline %s: %w", checkAgainst, err)
	}
	fresh := eval.ResultSet{Mode: mode, Train: trainSummary, HeldOut: heldOutSummary}
	problems := eval.CheckDrift(baseline, fresh)
	if len(problems) > 0 {
		fmt.Println("\nDRIFT DETECTED vs", checkAgainst)
		for _, p := range problems {
			fmt.Println(" -", p)
		}
		return fmt.Errorf("%d metric(s) drifted beyond tolerance", len(problems))
	}
	fmt.Println("\nno drift vs", checkAgainst)
	return nil
}

func printSummary(name string, s eval.Summary) {
	fmt.Printf("%-10s %6d %12.4f %12.4f %7.1f%% %9.1f%% %9.1f%% %7.1f%%\n",
		name, s.N, s.RoutedCost, s.BaselineCost, s.CostSavingsPct(),
		s.Accuracy()*100, s.BaselineAccuracy()*100, s.CheapModelRecall*100)
}
