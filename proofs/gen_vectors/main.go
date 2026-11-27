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

// pair is one numerator and denominator to write.
type pair struct{ n, d int }

func main() {
	seen := map[pair]bool{}
	var all []pair
	add := func(n, d int) {
		p := pair{n, d}
		if n < 0 || n > d || seen[p] {
			return
		}
		seen[p] = true
		all = append(all, p)
	}

	// Every numerator for small denominators.
	for d := 1; d <= 32; d++ {
		for n := 0; n <= d; n++ {
			add(n, d)
		}
	}
	// Every exact halfway value up to 1000, and at 2000 where float64 drifts the most.
	// These are the lines that separate "round the ratio" from "round the float64":
	// before D22 the tool printed the other tenth for 553 of them.
	for d := 1; d <= 1000; d++ {
		for n := 0; n <= d; n++ {
			if isTie(n, d) {
				add(n, d)
			}
		}
	}
	for n := 0; n <= 2000; n++ {
		if isTie(n, 2000) {
			add(n, 2000)
		}
	}
	// A spread of larger denominators, in the shapes a run produces.
	for _, d := range []int{40, 48, 50, 60, 64, 80, 100, 120, 125, 128, 160, 200, 250, 256, 320, 400, 500, 512, 800, 1000, 1024} {
		for _, n := range []int{0, 1, d / 5, d / 4, d / 3, d / 2, 2 * d / 3, 3 * d / 4, d - 1, d} {
			add(n, d)
		}
	}

	sort.Slice(all, func(i, j int) bool {
		if all[i].d != all[j].d {
			return all[i].d < all[j].d
		}
		return all[i].n < all[j].n
	})

	var b strings.Builder
	for _, p := range all {
		fmt.Fprintf(&b, "%d,%d,%s\n", p.n, p.d, analysis.NewRate(p.n, p.d).Text)
	}
	if err := os.WriteFile(outPath, []byte(b.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d lines)\n", outPath, len(all))
}

// isTie reports whether 1000*n/d sits exactly halfway between two tenths, which is where
// the tie-break decides the printed digit.
func isTie(n, d int) bool {
	return (2000*n)%d == 0 && ((2000*n)/d)%2 == 1
}
