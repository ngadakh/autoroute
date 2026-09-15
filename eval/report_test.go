package eval

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngadakh/autoroute/internal/router"
)

func testSummary() Summary {
	return Summary{
		N: 100, RoutedCost: 1.5, BaselineCost: 3.0,
		RoutedScoreSum: 90, BaselineScoreSum: 95,
		CheapModelRecall: 0.75,
		ByLayer:          map[string]int{"L1": 60, "L2": 40},
	}
}

func TestWriteResultsMarkdown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "RESULTS.md")
	train, heldOut := testSummary(), testSummary()

	if err := WriteResultsMarkdown("L1+L2 (real ONNX embedder)", train, heldOut, path); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)

	for _, want := range []string{
		"# AutoRoute eval results",
		"L1+L2 (real ONNX embedder)",
		"mistralai/mistral-7b-chat",
		"gpt-4-1106-preview",
		"| train |",
		"| held-out |",
		"50.0%", // CostSavingsPct for the fixture (1 - 1.5/3.0)*100
		"RESULTS_chart.svg",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("RESULTS.md missing %q\n---\n%s", want, out)
		}
	}
}

func TestWriteChartSVG(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chart.svg")
	if err := WriteChartSVG(testSummary(), testSummary(), path); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var doc struct {
		XMLName xml.Name `xml:"svg"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("output isn't valid XML/SVG: %v\n---\n%s", err, b)
	}
}

func TestDefaultPathNoteFlagsLowL1AndHighConservative(t *testing.T) {
	s := Summary{N: 100, ByLayer: map[string]int{
		router.LayerL1:              1,
		router.LayerDegradedLowConf: 80,
		router.LayerL2:              19,
	}}
	note := defaultPathNote(s)
	if !strings.Contains(note, "not confidently discriminating") {
		t.Fatalf("expected the low-L1/high-conservative explanation, got:\n%s", note)
	}
}

func TestDefaultPathNoteConfidentMajority(t *testing.T) {
	s := Summary{N: 100, ByLayer: map[string]int{
		router.LayerL1:            60,
		router.LayerL2:            35,
		router.LayerL2BandDefault: 5,
	}}
	note := defaultPathNote(s)
	if strings.Contains(note, "not confidently discriminating") {
		t.Fatalf("did not expect the low-confidence explanation for a confident majority, got:\n%s", note)
	}
}

func TestSummaryZeroValueMath(t *testing.T) {
	var s Summary
	if s.Accuracy() != 0 || s.BaselineAccuracy() != 0 || s.CostSavingsPct() != 0 {
		t.Fatalf("zero-value Summary should have zero derived metrics, got %+v", s)
	}
}
