package qdrant

import (
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

// SparseVector is a {indices, values} pair as Qdrant expects sparse vectors.
type SparseVector struct {
	Indices []uint32  `json:"indices"`
	Values  []float32 `json:"values"`
}

// BM25Vector tokenizes text and returns a sparse vector keyed by FNV-32 hash
// of each unique term, with value = log(1+tf). Qdrant applies the IDF modifier
// server-side at query time, turning this into a proper BM25 score.
//
// We hash terms instead of maintaining a vocabulary so indexing is stateless —
// any chunk can be embedded independently without coordinating on a shared
// term-id mapping. FNV-32 collisions are rare at corpus sizes <100K terms and
// hybrid fusion (RRF) is robust to the occasional false positive.
func BM25Vector(text string) SparseVector {
	tokens := tokenize(text)
	if len(tokens) == 0 {
		return SparseVector{}
	}
	tf := make(map[uint32]int, len(tokens))
	for _, t := range tokens {
		tf[hashTerm(t)]++
	}
	indices := make([]uint32, 0, len(tf))
	values := make([]float32, 0, len(tf))
	for idx, count := range tf {
		indices = append(indices, idx)
		values = append(values, float32(math.Log1p(float64(count))))
	}
	return SparseVector{Indices: indices, Values: values}
}

// tokenize lowercases and splits text on non-letter/digit boundaries, dropping
// tokens shorter than 2 chars. Apostrophes are stripped (so "celestial's" →
// "celestials") to keep possessives matchable against bare nouns.
func tokenize(text string) []string {
	var (
		out  []string
		buf  strings.Builder
		emit = func() {
			if buf.Len() >= 2 {
				out = append(out, buf.String())
			}
			buf.Reset()
		}
	)
	for _, r := range strings.ToLower(text) {
		switch {
		case r == '\'' || r == '’': // straight + curly apostrophe — skip
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			buf.WriteRune(r)
		default:
			emit()
		}
	}
	emit()
	return out
}

func hashTerm(t string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(t))
	return h.Sum32()
}
