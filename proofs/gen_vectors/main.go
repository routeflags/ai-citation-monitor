//go:build ignore

// Command gen_vectors writes proofs/rate-vectors.csv from analysis.NewRate.
//
// The file is the bridge between the Go code and proofs/Rate.lean: both sides must
// reproduce every line. Run it from the repository root after changing the rounding
// or the formatting:
//
//	go run proofs/gen_vectors/main.go
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/routeflags/ai-citation-monitor/internal/analysis"
)

const outPath = "proofs/rate-vectors.csv"

func main() {
	// The vectors are the inputs a report actually sees: every numerator for small
	// denominators (which includes every exact-halfway case that can occur below 33)
	// plus a spread of larger denominators from real runs.
	dens := make([]int, 0, 64)
	for d := 1; d <= 32; d++ {
		dens = append(dens, d)
	}
	for _, d := range []int{40, 48, 50, 60, 64, 80, 100, 120, 125, 128, 160, 200, 250, 256, 320, 400, 500, 512, 800, 1000, 1024} {
		dens = append(dens, d)
	}

	var b strings.Builder
	for _, d := range dens {
		for _, n := range numerations(d) {
			fmt.Fprintf(&b, "%d,%d,%s\n", n, d, analysis.NewRate(n, d).Text)
		}
	}
	if err := os.WriteFile(outPath, []byte(b.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d lines)\n", outPath, strings.Count(b.String(), "\n"))
}

// numerations lists the numerators to write for one denominator. Small
// denominators get every numerator; larger ones get the shapes a run produces.
//
// 2000 is deliberately absent: 1000/2000 sits exactly halfway between 0.0 and 0.1,
// and float64 cannot hold that halfway point (it rounds to 0.05000...0277), so the
// tool prints 0.1% where exact rounding goes to the even tenth. That gap is
// documented in proofs/Rate.lean and docs/metrics.md instead of being a vector.
func numerations(d int) []int {
	if d <= 32 {
		ns := make([]int, 0, d+1)
		for n := 0; n <= d; n++ {
			ns = append(ns, n)
		}
		return ns
	}
	set := map[int]bool{0: true, 1: true, d - 1: true, d: true}
	for _, n := range []int{d / 5, d / 4, d / 3, d / 2, 2 * d / 3, 3 * d / 4} {
		set[n] = true
	}
	ns := make([]int, 0, len(set))
	for n := range set {
		if n >= 0 && n <= d {
			ns = append(ns, n)
		}
	}
	sort.Ints(ns)
	return ns
}
