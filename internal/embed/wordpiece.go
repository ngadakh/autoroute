// Package embed turns text into a normalised sentence-embedding vector using a
// small transformer model run in-process via ONNX Runtime. No network calls, no
// embedding API. This is the L2 layer of the router pipeline.
package embed

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode"
)

// Special tokens for a BERT-family uncased WordPiece vocabulary.
const (
	tokCLS = "[CLS]"
	tokSEP = "[SEP]"
	tokPAD = "[PAD]"
	tokUNK = "[UNK]"
)

// Tokenizer is a pragmatic BERT WordPiece tokenizer for uncased models
// (all-MiniLM-L6-v2 and friends). It implements the pieces that matter for
// semantic routing: lowercase, whitespace + punctuation splitting, and greedy
// longest-match WordPiece. It deliberately skips accent stripping and CJK
// handling — see SPIKE.md for why that is acceptable at this layer.
type Tokenizer struct {
	vocab      map[string]int64
	unkID      int64
	clsID      int64
	sepID      int64
	padID      int64
	maxSeqLen  int
	maxSubword int // longest key in vocab, bounds the WordPiece inner loop
}

// LoadTokenizer reads a HuggingFace vocab.txt (one token per line, id == line
// number) and returns a tokenizer with the given maximum sequence length.
func LoadTokenizer(vocabPath string, maxSeqLen int) (*Tokenizer, error) {
	f, err := os.Open(vocabPath)
	if err != nil {
		return nil, fmt.Errorf("open vocab: %w", err)
	}
	defer f.Close()

	t := &Tokenizer{
		vocab:     make(map[string]int64, 32000),
		maxSeqLen: maxSeqLen,
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var id int64
	for sc.Scan() {
		tok := strings.TrimRight(sc.Text(), "\r\n")
		t.vocab[tok] = id
		if n := len(tok); n > t.maxSubword {
			t.maxSubword = n
		}
		id++
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read vocab: %w", err)
	}

	for name, dst := range map[string]*int64{
		tokUNK: &t.unkID, tokCLS: &t.clsID, tokSEP: &t.sepID, tokPAD: &t.padID,
	} {
		v, ok := t.vocab[name]
		if !ok {
			return nil, fmt.Errorf("vocab missing required token %q", name)
		}
		*dst = v
	}
	return t, nil
}

// Encoded is the model-ready form of one text.
type Encoded struct {
	InputIDs      []int64
	AttentionMask []int64
	TokenTypeIDs  []int64
}

// Len is the sequence length (all three slices share it).
func (e Encoded) Len() int { return len(e.InputIDs) }

// Encode lowercases, splits, WordPiece-tokenises, wraps with [CLS]/[SEP] and
// truncates to maxSeqLen. The sequence is not padded — callers embed one text at
// a time, so a ragged length is fine and avoids wasted compute on PAD tokens.
func (t *Tokenizer) Encode(text string) Encoded {
	pieces := make([]int64, 0, 32)
	pieces = append(pieces, t.clsID)

	// -2 leaves room for [CLS] and [SEP].
	budget := t.maxSeqLen - 2
	for _, word := range basicSplit(text) {
		if len(pieces)-1 >= budget {
			break
		}
		for _, sub := range t.wordPiece(word) {
			if len(pieces)-1 >= budget {
				break
			}
			pieces = append(pieces, sub)
		}
	}
	pieces = append(pieces, t.sepID)

	mask := make([]int64, len(pieces))
	types := make([]int64, len(pieces))
	for i := range mask {
		mask[i] = 1
	}
	return Encoded{InputIDs: pieces, AttentionMask: mask, TokenTypeIDs: types}
}

// basicSplit lowercases, splits on Unicode whitespace, and peels punctuation
// runes off into their own tokens — matching BERT's BasicTokenizer closely
// enough for embedding similarity.
func basicSplit(text string) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range strings.ToLower(strings.TrimSpace(text)) {
		switch {
		case unicode.IsSpace(r):
			flush()
		case isPunct(r):
			flush()
			out = append(out, string(r))
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return out
}

func isPunct(r rune) bool {
	if (r >= '!' && r <= '/') || (r >= ':' && r <= '@') ||
		(r >= '[' && r <= '`') || (r >= '{' && r <= '~') {
		return true
	}
	return unicode.IsPunct(r) || unicode.IsSymbol(r)
}

// wordPiece greedily matches the longest vocabulary entry from the start of the
// (rune) word, emitting "##"-prefixed continuation pieces. A word with any
// unmatched span becomes a single [UNK].
func (t *Tokenizer) wordPiece(word string) []int64 {
	runes := []rune(word)
	if len(runes) == 0 {
		return nil
	}

	var out []int64
	start := 0
	for start < len(runes) {
		end := len(runes)
		var curID int64 = -1
		for end > start {
			sub := string(runes[start:end])
			if start > 0 {
				sub = "##" + sub
			}
			if len(sub) <= t.maxSubword {
				if id, ok := t.vocab[sub]; ok {
					curID = id
					break
				}
			}
			end--
		}
		if curID < 0 {
			return []int64{t.unkID}
		}
		out = append(out, curID)
		start = end
	}
	return out
}
