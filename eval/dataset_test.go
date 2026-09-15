package eval

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCSV(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "data.csv")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const fixtureCSV = `sample_id,prompt,eval_name,oracle_model_to_route_to,score:cheap-model,score:frontier-model,cost:cheap-model,cost:frontier-model
s1,who is the pm of india,factual,cheap-model,1,1,0.0001,0.01
s2,prove this theorem,math,frontier-model,0,1,0.0001,0.01
s3,another factual one,factual,cheap-model,1,1,0.0001,0.01
`

func TestLoadCSV(t *testing.T) {
	rows, err := LoadCSV(writeCSV(t, fixtureCSV))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	r := rows[0]
	if r.SampleID != "s1" || r.Prompt != "who is the pm of india" || r.EvalName != "factual" || r.Oracle != "cheap-model" {
		t.Fatalf("row 0 = %+v", r)
	}
	if r.Score["cheap-model"] != 1 || r.Cost["frontier-model"] != 0.01 {
		t.Fatalf("row 0 score/cost = %+v / %+v", r.Score, r.Cost)
	}
}

func TestLoadCSVMissingRequiredColumn(t *testing.T) {
	body := "sample_id,prompt\ns1,hi\n"
	if _, err := LoadCSV(writeCSV(t, body)); err == nil {
		t.Fatal("expected an error for missing required columns")
	}
}

func TestLoadCSVNoScoreColumns(t *testing.T) {
	body := "sample_id,prompt,eval_name,oracle_model_to_route_to\ns1,hi,x,m\n"
	if _, err := LoadCSV(writeCSV(t, body)); err == nil {
		t.Fatal("expected an error when no score: columns are present")
	}
}

func TestLoadCSVMalformedNumber(t *testing.T) {
	body := "sample_id,prompt,eval_name,oracle_model_to_route_to,score:m,cost:m\ns1,hi,x,m,not-a-number,0.01\n"
	if _, err := LoadCSV(writeCSV(t, body)); err == nil {
		t.Fatal("expected an error for a malformed score value")
	}
}

func TestLoadCSVMissingFile(t *testing.T) {
	if _, err := LoadCSV("/no/such/file.csv"); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}
