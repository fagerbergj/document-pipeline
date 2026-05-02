package qdrant

import (
	"reflect"
	"sort"
	"testing"
)

func TestTokenize(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"Celestial Sword (the ravine)", []string{"celestial", "sword", "the", "ravine"}},
		{"Celestial's Sword — Electra's blade.", []string{"celestials", "sword", "electras", "blade"}},
		{"a is to or", []string{"is", "to", "or"}}, // single-char dropped
		{"", nil},
		{"--- ___ +++", nil},
		{"foo, bar.baz/qux", []string{"foo", "bar", "baz", "qux"}},
	}
	for _, tc := range cases {
		got := tokenize(tc.in)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("tokenize(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestBM25Vector_HitsExpectedTerms(t *testing.T) {
	sv := BM25Vector("Celestial Sword the ravine Celestial")
	if len(sv.Indices) != len(sv.Values) {
		t.Fatalf("indices and values length mismatch: %d vs %d", len(sv.Indices), len(sv.Values))
	}
	// 4 unique terms: celestial, sword, the, ravine.
	if len(sv.Indices) != 4 {
		t.Errorf("expected 4 unique terms, got %d", len(sv.Indices))
	}
	// "celestial" appears twice → log(1+2) ≈ 1.0986; the others have value log(1+1) ≈ 0.6931.
	// Sort values to verify.
	values := append([]float32(nil), sv.Values...)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	if values[0] >= values[len(values)-1] {
		t.Errorf("expected repeated term to score higher: %v", values)
	}
}

func TestBM25Vector_EmptyText(t *testing.T) {
	sv := BM25Vector("")
	if len(sv.Indices) != 0 || len(sv.Values) != 0 {
		t.Errorf("empty text should produce empty sparse vector, got %d indices", len(sv.Indices))
	}
}
