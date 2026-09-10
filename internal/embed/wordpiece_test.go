package embed

import (
	"os"
	"path/filepath"
	"testing"
)

// vocabPath locates the fetched vocab; the test skips if setup hasn't run.
func vocabPath(t *testing.T) string {
	t.Helper()
	p := filepath.Join("..", "..", "models", "all-MiniLM-L6-v2", "vocab.txt")
	if _, err := os.Stat(p); err != nil {
		t.Skipf("vocab not present (run `make setup`): %v", err)
	}
	return p
}

func TestBasicSplit(t *testing.T) {
	got := basicSplit("  Who is the PM of India? ")
	want := []string{"who", "is", "the", "pm", "of", "india", "?"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("token %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestEncodeWrapsAndBounds(t *testing.T) {
	tok, err := LoadTokenizer(vocabPath(t), 16)
	if err != nil {
		t.Fatal(err)
	}
	enc := tok.Encode("this is a reasonably ordinary sentence about routing and models and latency budgets")
	if enc.Len() > 16 {
		t.Fatalf("sequence length %d exceeds max 16", enc.Len())
	}
	if enc.InputIDs[0] != tok.clsID {
		t.Fatalf("first token %d is not [CLS] %d", enc.InputIDs[0], tok.clsID)
	}
	if enc.InputIDs[enc.Len()-1] != tok.sepID {
		t.Fatalf("last token %d is not [SEP] %d", enc.InputIDs[enc.Len()-1], tok.sepID)
	}
	if len(enc.AttentionMask) != enc.Len() || len(enc.TokenTypeIDs) != enc.Len() {
		t.Fatal("mask/type length mismatch")
	}
}

func TestWordPieceKnownTokens(t *testing.T) {
	tok, err := LoadTokenizer(vocabPath(t), 128)
	if err != nil {
		t.Fatal(err)
	}
	// "routing" is a single entry in the bert-base-uncased vocab; "kubernetes"
	// is not and must split into >1 "##"-continued piece with no [UNK].
	if got := tok.wordPiece("routing"); len(got) != 1 {
		t.Fatalf("routing -> %d pieces, want 1", len(got))
	}
	pieces := tok.wordPiece("kubernetes")
	if len(pieces) < 2 {
		t.Fatalf("kubernetes -> %d pieces, want >=2", len(pieces))
	}
	for _, p := range pieces {
		if p == tok.unkID {
			t.Fatal("kubernetes produced [UNK]")
		}
	}
}
