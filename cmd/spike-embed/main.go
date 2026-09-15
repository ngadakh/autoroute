// Command spike-embed is the M0 de-risking spike: it proves that a
// sentence-embedding model runs in-process in Go via ONNX Runtime, produces
// sensible routing decisions, and does so fast enough to sit on the request
// path. Run `make setup` first to fetch the model and runtime.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/ngadakh/autoroute/internal/embed"
	"github.com/ngadakh/autoroute/internal/router"
)

type sample struct {
	name   string
	prompt string
	want   router.Tier
}

// The six worked examples from the architecture doc, minus the failure/shadow
// cases which are not routing decisions.
var samples = []sample{
	{"A trivial-lookup", "What is the capital of France?", router.TierCheap},
	{"B formal-proof", "Prove that the sum of the first n odd numbers equals n squared.", router.TierFrontier},
	{"C code-edit", "Make this function idiomatic and add type hints: def f(x): return [i for i in x if i%2==0]", router.TierMid},
	{"D open-judgement", "Here's my plan for a B2B pricing change - switching from seats to usage. Is this a good idea?", router.TierFrontier},
	{"E short-rewrite", "Rewrite this sentence to sound more formal.", router.TierCheap},
	{"F extraction", "Extract every date mentioned in this text and return them as JSON.", router.TierMid},
}

func main() {
	lib := flag.String("lib", "third_party/onnxruntime/lib/libonnxruntime.dylib", "path to the ONNX Runtime shared library")
	model := flag.String("model", "models/all-MiniLM-L6-v2/model.onnx", "path to the ONNX model")
	vocab := flag.String("vocab", "models/all-MiniLM-L6-v2/vocab.txt", "path to vocab.txt")
	maxSeq := flag.Int("maxseq", 128, "maximum sequence length")
	runs := flag.Int("runs", 300, "latency benchmark iterations")
	flag.Parse()

	if err := run(*lib, *model, *vocab, *maxSeq, *runs); err != nil {
		fmt.Fprintf(os.Stderr, "\nspike failed: %v\n", err)
		os.Exit(1)
	}
}

func run(lib, model, vocab string, maxSeq, runs int) error {
	if err := embed.InitRuntime(lib); err != nil {
		return fmt.Errorf("init ONNX Runtime (is %s present? run `make setup`): %w", lib, err)
	}

	t0 := time.Now()
	e, err := embed.NewEmbedder(model, vocab, maxSeq)
	if err != nil {
		return err
	}
	defer e.Close()
	fmt.Printf("model loaded in %v  (output tensor: %q)\n", time.Since(t0).Round(time.Millisecond), e.ModelOutputName())

	t0 = time.Now()
	clf, err := router.NewClassifier(e, router.Routes)
	if err != nil {
		return err
	}
	nEx := 0
	for _, r := range router.Routes {
		nEx += len(r.Exemplars)
	}
	fmt.Printf("classifier built from %d exemplars in %v\n\n", nEx, time.Since(t0).Round(time.Millisecond))

	// --- routing decisions -------------------------------------------------
	fmt.Println("ROUTING DECISIONS")
	fmt.Println("-----------------")
	pass := 0
	for _, s := range samples {
		vec, err := e.Embed(s.prompt)
		if err != nil {
			return err
		}
		d := clf.Classify(vec)
		ok := d.Tier == s.want
		if ok {
			pass++
		}
		mark := "ok  "
		if !ok {
			mark = "MISS"
		}
		fmt.Printf("[%s] %-18s -> %-9s via %-22s conf %.2f   (want %s)\n",
			mark, s.name, d.Tier, d.Route, d.Confidence, s.want)
		top3 := d.Scores
		if len(top3) > 3 {
			top3 = top3[:3]
		}
		for _, rs := range top3 {
			fmt.Printf("        %-22s %.3f  (%s)\n", rs.Route, rs.Score, rs.Tier)
		}
	}
	fmt.Printf("\ndecision agreement with hand-labels: %d/%d\n\n", pass, len(samples))

	// --- latency ---------------------------------------------------------
	fmt.Println("EMBED LATENCY (single prompt, in-process, CPU)")
	fmt.Println("---------------------------------------------")
	probe := "Summarise the tradeoffs of moving this workload to Kubernetes."

	tCold := time.Now()
	if _, err := e.Embed(probe); err != nil {
		return err
	}
	cold := time.Since(tCold)

	for i := 0; i < 20; i++ { // warm up
		if _, err := e.Embed(probe); err != nil {
			return err
		}
	}
	ds := make([]time.Duration, runs)
	for i := 0; i < runs; i++ {
		t := time.Now()
		if _, err := e.Embed(probe); err != nil {
			return err
		}
		ds[i] = time.Since(t)
	}
	sort.Slice(ds, func(a, b int) bool { return ds[a] < ds[b] })
	fmt.Printf("cold call : %v\n", cold.Round(10*time.Microsecond))
	fmt.Printf("warm p50  : %v\n", ds[runs/2].Round(10*time.Microsecond))
	fmt.Printf("warm p90  : %v\n", ds[runs*90/100].Round(10*time.Microsecond))
	fmt.Printf("warm p99  : %v\n", ds[min(runs-1, runs*99/100)].Round(10*time.Microsecond))
	fmt.Printf("warm max  : %v\n", ds[runs-1].Round(10*time.Microsecond))

	fmt.Printf("\nverdict: %d/%d decisions agree with hand-labels; warm p50 %v.\n",
		pass, len(samples), ds[runs/2].Round(10*time.Microsecond))
	return nil
}
