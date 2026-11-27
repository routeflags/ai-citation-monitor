package analysis

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"testing"
)

// proofs/rate-vectors.csv is the bridge between this package and proofs/Rate.lean:
// both sides must reproduce every line. The Lean file proves the rounding rule and the
// bounds around it; this test keeps the Go side on the same rule.
//
// `make proof` runs the other half. Regenerate the file with
// `go run proofs/gen_vectors/main.go` when the formatting changes.
func TestRateDisplayMatchesProofVectors(t *testing.T) {
	f, err := os.Open("../../proofs/rate-vectors.csv")
	if err != nil {
		t.Fatalf("open proofs/rate-vectors.csv: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	line, checked := 0, 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		parts := strings.Split(text, ",")
		if len(parts) != 3 {
			t.Fatalf("line %d: want n,d,text, got %q", line, text)
		}
		n, err := strconv.Atoi(parts[0])
		if err != nil {
			t.Fatalf("line %d: n: %v", line, err)
		}
		d, err := strconv.Atoi(parts[1])
		if err != nil {
			t.Fatalf("line %d: d: %v", line, err)
		}
		if got := NewRate(n, d).Text; got != parts[2] {
			t.Errorf("NewRate(%d, %d).Text = %q, want %q (proofs/rate-vectors.csv line %d)",
				n, d, got, parts[2], line)
		}
		checked++
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read proofs/rate-vectors.csv: %v", err)
	}
	if checked == 0 {
		t.Fatal("no vectors were read: proofs/rate-vectors.csv is empty")
	}
}
