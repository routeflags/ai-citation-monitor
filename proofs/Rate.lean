/-
The arithmetic of the report, as proved statements.

`internal/analysis` prints every number as `20.0% (n=1/5)`, prints a difference between
two measurements in percentage points, and divides the domain-share column by the
group's citation total. This file writes those rules down and proves what a reader can
check by hand:

  * a rate never prints more than `100.0%` (`tenths_le_1000`);
  * a printed percentage is within half a tenth of the exact ratio
    (`tenths_error`);
  * a share column adds up to `100%` within half a tenth per row
    (`share_column_bound`);
  * a difference in percentage points composes (`delta_add`).

The proof is about the definitions below. What ties them to the Go code is
`rate-vectors.csv`: `lean --run Rate.lean` checks these definitions against every line
of it, and `internal/analysis/vectors_test.go` checks `NewRate` against the same file.

One case is documented rather than proved: a value that sits exactly halfway between
two tenths goes to the even tenth, but `float64` cannot hold every halfway point
(`1000/2000`), so the tool prints `0.1%` where the exact rule gives `0.0%`. See
`docs/metrics.md`.

Run from this directory:

    lean Rate.lean          # check the proofs
    lean --run Rate.lean    # check the proofs and the vectors against the model
-/

namespace Aicm

/-- `1000 * n / d` rounded to the nearest integer, an exact halfway value going to the
even integer: the rule `fmt.Sprintf("%.1f", 100 * n / d)` applies. Kept literally as
the Go code does it — quotient, remainder, then the tie — so the two can be compared
line by line. -/
def tenths (n d : Nat) : Nat :=
  let q := 1000 * n / d
  let r := 1000 * n % d
  if 2 * r < d then q
  else if d < 2 * r then q + 1
  else if q % 2 = 0 then q else q + 1

/-- The four cases of `tenths`, as one lemma about the quotient and the remainder. The
`q` and `r` are plain variables here so the arithmetic stays linear; the wrapper below
is what the report actually calls. -/
private theorem spec_round (n d q r : Nat) (hqr : q * d + r = 1000 * n) (hr : r < d) :
    let k := if 2 * r < d then q
      else if d < 2 * r then q + 1
      else if q % 2 = 0 then q else q + 1
    2 * d * k ≤ 2000 * n + d ∧ 2000 * n + d ≤ 2 * d * (k + 1) := by
  dsimp only
  have h2q : 2 * (q * d) + 2 * r = 2000 * n := by omega
  have e1 : (2 : Nat) * d * q = 2 * (q * d) := by rw [Nat.mul_assoc, Nat.mul_comm d q]
  have e2 : (2 : Nat) * d * (q + 1) = 2 * (q * d) + 2 * d := by
    rw [Nat.mul_add, Nat.mul_one, e1]
  have e3 : (2 : Nat) * d * (q + 1 + 1) = 2 * (q * d) + 4 * d := by
    rw [Nat.mul_add, Nat.mul_one, e2]
    omega
  have hdown : 2 * r ≤ d →
      2 * d * q ≤ 2000 * n + d ∧ 2000 * n + d ≤ 2 * d * (q + 1) := by
    intro h
    rw [e1, e2]
    omega
  have hup : d ≤ 2 * r →
      2 * d * (q + 1) ≤ 2000 * n + d ∧ 2000 * n + d ≤ 2 * d * (q + 1 + 1) := by
    intro h
    rw [e2, e3]
    omega
  by_cases h1 : 2 * r < d
  · rw [ite_eq_left h1]
    exact hdown (Nat.le_of_lt h1)
  · rw [ite_eq_right h1]
    by_cases h2 : d < 2 * r
    · rw [ite_eq_left h2]
      exact hup (Nat.le_of_lt h2)
    · rw [ite_eq_right h2]
      have h3 : 2 * r = d := Nat.le_antisymm (Nat.le_of_not_lt h2) (Nat.le_of_not_lt h1)
      by_cases h4 : q % 2 = 0
      · rw [ite_eq_left h4]
        exact hdown (Nat.le_of_eq h3)
      · rw [ite_eq_right h4]
        exact hup (Nat.le_of_eq h3.symm)

/-- The rounding interval: the displayed tenths sit within half a tenth of the exact
ratio, measured in the numerator's units. Both bounds are weak on purpose — at an exact
halfway value the tie-break decides which one is tight, and that is the one thing the
vectors cover instead. -/
theorem tenths_spec (n d : Nat) (hd : 0 < d) :
    2 * d * tenths n d ≤ 2000 * n + d ∧ 2000 * n + d ≤ 2 * d * (tenths n d + 1) := by
  have h := spec_round n d (1000 * n / d) (1000 * n % d)
    (by rw [Nat.mul_comm]; exact Nat.div_add_mod (1000 * n) d)
    (Nat.mod_lt (1000 * n) hd)
  simpa only [tenths] using h

/-- `n` counts a subset of `d` (a cited run out of the judgeable ones), so the rate
never prints more than `100.0%`. Calling it with a larger numerator is a bug in the
aggregation, not a rounding question. -/
theorem tenths_le_1000 (n d : Nat) (hnd : n ≤ d) (hd : 0 < d) : tenths n d ≤ 1000 := by
  have h := (tenths_spec n d hd).1
  have h2001 : 2000 * n + d ≤ 2001 * d := by omega
  have hmul : (2 * tenths n d) * d ≤ 2001 * d := by
    have hkey : (2 * tenths n d) * d = 2 * d * tenths n d := by
      rw [Nat.mul_assoc, Nat.mul_comm (tenths n d) d, ← Nat.mul_assoc]
    rw [hkey]
    omega
  have hc := Nat.le_of_mul_le_mul_right hmul hd
  omega

/-- The printed percentage is within half a tenth of the exact ratio: the error of
`tenths n d` against `1000 * n / d`, scaled by `2 * d`. -/
theorem tenths_error (n d : Nat) (hd : 0 < d) :
    -(d : Int) ≤ 2 * ((d : Int) * (tenths n d : Int)) - 2000 * (n : Int) ∧
      2 * ((d : Int) * (tenths n d : Int)) - 2000 * (n : Int) ≤ (d : Int) := by
  obtain ⟨h1, h2⟩ := tenths_spec n d hd
  have e : (2 : Nat) * d * (tenths n d + 1) = 2 * (d * tenths n d) + 2 * d := by
    rw [Nat.mul_add, Nat.mul_one, Nat.mul_assoc]
  have h2' : 2000 * n + d ≤ 2 * (d * tenths n d) + 2 * d := by
    rw [← e]
    exact h2
  have c1 : 2 * ((d : Int) * (tenths n d : Int)) ≤
      2000 * (n : Int) + (d : Int) := by
    have h := Int.ofNat_le.mpr h1
    simpa [Int.natCast_mul, Int.natCast_add, Int.mul_assoc] using h
  have c2 : 2000 * (n : Int) + (d : Int) ≤
      2 * ((d : Int) * (tenths n d : Int)) + 2 * (d : Int) := by
    have h := Int.ofNat_le.mpr h2'
    simpa [Int.natCast_mul, Int.natCast_add, Int.mul_assoc] using h
  constructor <;> omega

/-- A difference between two measurements is in percentage points: it composes, which
is what lets a report compare `A -> C` with `A -> B -> C`, and it is antisymmetric. -/
def deltaTenths (before after : Int) : Int := after - before

theorem delta_add (a b c : Int) : deltaTenths a c = deltaTenths a b + deltaTenths b c := by
  unfold deltaTenths
  omega

theorem delta_neg (a b : Int) : deltaTenths a b = -deltaTenths b a := by
  unfold deltaTenths
  omega

/-- Splitting the displayed share of two rows, so the proof can add the rounding error
row by row. -/
private theorem share_error_split (T x y : Int) :
    2 * (T * (x + y)) = 2 * (T * x) + 2 * (T * y) := by
  rw [Int.mul_add, Int.mul_add]

/-- The column's error against the subtotal `S` it has accumulated so far: each row is
divided by the group total `T`, so the error is bounded by half a tenth per row no
matter how many rows there are. -/
private theorem share_column_aux (T : Nat) (hT : 0 < T) :
    ∀ cs : List Nat, ∀ S : Nat, cs.sum = S → (∀ c ∈ cs, c ≤ T) →
      -((((cs.length * T : Nat)) : Int)) ≤
          2 * ((T : Int) * ((cs.map (fun c => (tenths c T : Int))).sum)) - 2000 * (S : Int) ∧
        2 * ((T : Int) * ((cs.map (fun c => (tenths c T : Int))).sum)) - 2000 * (S : Int) ≤
          (((cs.length * T : Nat)) : Int) := by
  intro cs
  induction cs with
  | nil =>
    intro S hS _
    simp only [List.sum_nil] at hS
    rw [← hS]
    simp
  | cons c rest ih =>
    intro S hS hle
    have hS' : rest.sum = S - c := by
      have h : c + rest.sum = S := by simpa [List.sum_cons] using hS
      omega
    have hcS : c ≤ S := by
      have h : c + rest.sum = S := by simpa [List.sum_cons] using hS
      omega
    have hrest := ih (S - c) hS' (fun x hx => hle x (by simp [hx]))
    obtain ⟨rest1, rest2⟩ := hrest
    obtain ⟨row1, row2⟩ := tenths_error c T hT
    have hcast : ((((S - c : Nat))) : Int) = (S : Int) - (c : Int) := Int.natCast_sub hcS
    rw [hcast] at rest1 rest2
    have hlen : (((((c :: rest).length) * T : Nat)) : Int) =
        (((rest.length * T : Nat)) : Int) + (T : Int) := by
      simp only [List.length_cons, Nat.add_mul, Nat.one_mul, Int.natCast_add]
    simp only [List.map_cons, List.sum_cons]
    rw [share_error_split, hlen]
    constructor <;> omega

/-- The domain-share column, summed over its rows, sits within half a tenth of `100%`
per row: `|sum - 1000| <= rows / 2` in tenths. This is why the report never prints a
`100.0%` total for that column. -/
theorem share_column_bound (T : Nat) (hT : 0 < T) (cs : List Nat) (hsum : cs.sum = T)
    (hle : ∀ c ∈ cs, c ≤ T) :
    -((((cs.length * T : Nat)) : Int)) ≤
        2 * ((T : Int) * ((cs.map (fun c => (tenths c T : Int))).sum)) - 2000 * (T : Int) ∧
      2 * ((T : Int) * ((cs.map (fun c => (tenths c T : Int))).sum)) - 2000 * (T : Int) ≤
        (((cs.length * T : Nat)) : Int) :=
  share_column_aux T hT cs T hsum hle

/-- The report's numbers, checked against the file the Go test reads. -/
def checkVectors : IO Unit := do
  let content ← IO.FS.readFile "rate-vectors.csv"
  let mut checked := 0
  let mut failures := 0
  for line in content.splitOn "\n" do
    let line := line.trimAscii.toString
    if line.isEmpty then continue
    match line.splitOn "," with
    | [ns, ds, shown] =>
      match ns.toNat?, ds.toNat? with
      | some n, some d =>
        checked := checked + 1
        let k := tenths n d
        let model := s!"{k / 10}.{k % 10}%"
        if model ≠ shown then
          failures := failures + 1
          IO.println s!"mismatch n={n} d={d}: model {model}, code {shown}"
      | _, _ =>
        failures := failures + 1
        IO.println s!"unparsable line: {line}"
    | _ =>
      failures := failures + 1
      IO.println s!"unparsable line: {line}"
  if failures == 0 then
    IO.println s!"{checked} rate vectors agree with the model"
  else
    IO.eprintln s!"{failures} of {checked} rate vectors disagree with the model"
    IO.Process.exit 1

end Aicm

/-- `lean --run Rate.lean` checks the vectors. Outside the namespace: the interpreter
looks for `main` at the root. -/
def main : IO Unit := Aicm.checkVectors
