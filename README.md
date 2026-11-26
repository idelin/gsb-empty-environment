# `stlf`: nested speculative-forwarding leak detection

`stlf` is a self-contained Go model of a small out-of-order processor with
partial-address Store-to-Load Forwarding (STLF).  It detects speculative leaks
that survive architectural rollback and verifies minimal speculation-barrier
patches against a full-state reference model.

The package is importable:

```go
import stlf "formal.local/w3b-984d40600f"
```

It was written for the following setting:

> Simultaneous partial-address STLF and up to two nested branch mispredictions
> can leave un-retracted cache observations even though the committed
> registers and memory have rolled back.  A `probe(reg)` observation is
> partitioned by `reg & 0x0f`.  Public inputs must match in a witness while the
> secret differs; the committed probe traces must be word-for-word equal while
> the speculative observation traces differ.

## Model

- 12-bit physical addresses (`0x000..0xfff`) and 8-bit bytes/registers.
- 16 registers (`r0..r15`) and 4096 bytes of memory.
- At most four uncommitted store-buffer entries.
- At most 20 custom instructions:
  `store`, `load`, `xor`, `cmp_branch`, `probe(reg)`, `barrier`.
- 16 read-only observation classes indexed by `reg & 0x0f`.
- The architectural path follows actual branch directions.  A load hits the
  newest buffered store with the exact same 12-bit address, otherwise memory.
- The speculative path follows each branch's static `PredictedTaken` bit for at
  most two nested wrong branches.
- A speculative load forwards from the newest store whose low six address bits
  match.  If the full 12-bit address also matches, the forward is precise.
  If the high six bits differ, the forwarded value remains visible for at most
  four cycles (`load_cycle+4`), after which the speculative window rolls back.
- `probe` observations are read-only and are never rolled back.
- `barrier` drains pending reads and unresolved branches and blocks later
  speculation in that window.

Two inputs form a speculative witness when:

1. `PublicLow6` is identical and `Secret` differs;
2. the committed architectural probe traces are equal; and
3. the speculative observation traces differ.

Pairs whose committed traces already differ are reported separately as native
architectural leaks.  A search timeout returns `StatusUnknown`; it is never
treated as safe.

`FindWitness` returns the shortest witness under `SearchOptions.Metric`
(`CostSteps` by default); `SearchOptions.RequirePartialForward` restricts the
search to pairs that actually exercised low-six-bit STLF.  `Witness.Cost`
reports steps, probe count, and first-divergence position.

## Quick start

```go
package main

import (
    "fmt"
    "time"

    stlf "formal.local/w3b-984d40600f"
)

func main() {
    host := stlf.ExampleHost()
    code := stlf.ExampleProgram()

    result := stlf.FindWitness(host, code, stlf.SearchOptions{
        Timeout: 5 * time.Second,
    })
    fmt.Println(result.Status) // Witness
    fmt.Println(result.Witness.A.ArchTrace, result.Witness.B.ArchTrace)
    fmt.Println(result.Witness.A.SpecTrace, result.Witness.B.SpecTrace)

    patch, report := stlf.FindMinimalBarrierPatch(host, code)
    fmt.Println(patch)                     // barrier@14
    fmt.Println(report.Valid)              // true
    fmt.Println(report.LeakFree)           // true
    fmt.Println(report.SemanticsPreserved) // true
}
```

The bundled 19-instruction attack uses:

- `r0` for the public six-bit address part;
- `0x405` for the secret slot;
- a wrong-path `load` of the secret;
- an uncommitted `store` to `0x40a`;
- a load from `0x00a`, which has the same low six bits but different high six
  bits, causing partial forwarding;
- `probe(r2)` inside the four-cycle window.

Its committed trace is empty (or a public-only `[15]`) for every secret, while
the speculative trace contains `secret & 0x0f`.

## API

Core entry points:

```go
func FindWitness(host Host, code []Instruction, opts ...SearchOptions) SearchResult
func Execute(host Host, code []Instruction, in Input, opts ...RunOptions) Execution
func RunArchitectural(host Host, code []Instruction, in Input, opts ...RunOptions) Execution
func RunSpeculative(host Host, code []Instruction, in Input, opts ...RunOptions) Execution
func ReferenceRun(host Host, code []Instruction, in Input, opts ...RunOptions) (MachineState, error)

func InsertBarriers(code []Instruction, patch Patch) ([]Instruction, error)
func VerifyBarrierPatch(host Host, code []Instruction, patch Patch, opts ...VerifyOptions) PatchReport
func FindMinimalBarrierPatch(host Host, code []Instruction, opts ...PatchSearchOptions) (Patch, PatchReport)
```

`Verifier` binds a host and program:

```go
v, err := stlf.NewVerifier(host, code)
res := v.FindWitness()
report := v.VerifyPatch(stlf.NewPatch(14))
patch, report := v.MinimalPatch()
```

The text assembler accepts labels, bracketed addresses, and optional branch
prediction:

```asm
start:
    cmp_branch r0, 0x00, leak, taken
    probe(r3)
    cmp_branch r4, r4, done, taken
leak:
    load  r1, [0x405]
    store r1, [0x40a]
    load  r2, [0x00a]
    probe r2
done:
    barrier
```

Addresses may be `[imm]`, `[reg]`, or `[imm + reg]`.

## Barrier verification

`InsertBarriers` repairs every branch target after insertion.  Without this
repair, adding an instruction would silently redirect branches and violate the
"do not change architectural semantics" requirement.

`VerifyBarrierPatch` runs, for every input in the selected domain:

- the precise full-state reference model on the original and patched programs;
- a field-by-field comparison of registers, memory, store buffer, PC, and
  committed probe trace;
- the speculative model on the patched program to detect any remaining
  same-architecture/different-speculation pair.

The report separates:

- `SemanticsPreserved`;
- `LeakFree` (no speculative leak);
- `NativeLeakFree` (no committed architectural leak);
- `Valid` (all three).

Because a barrier inserted at site `0` drains the speculative window before any
instruction, a semantics-preserving program with no native architectural leak
always has a one-barrier patch.  `FindMinimalBarrierPatch` therefore searches
zero- and one-site patches, trying later sites first so that speculation is
preserved as long as possible.  With the bundled example it returns
`barrier@14`, immediately before the leaking probe.

## Command line

```sh
go run ./cmd/stlfcheck -mode demo
go run ./cmd/stlfcheck -mode demo -json

go run ./cmd/stlfcheck -mode analyze -file program.asm \
  -high6 0x10 -secret-slot 0x405 -public-reg 0 -timeout 5s

go run ./cmd/stlfcheck -mode minimal -file program.asm
go run ./cmd/stlfcheck -mode verify -file program.asm -positions 14
```

## Tests

```sh
go test ./...
```

The tests cover exact architectural load matching, low-six-bit partial
forwarding, the four-cycle visibility window, two-level nested misprediction,
barrier draining, native-vs-speculative leak separation, branch-target repair,
the bundled witness, and full-state patch verification.
