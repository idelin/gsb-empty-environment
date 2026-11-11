# packetvm

`packetvm` is an importable Go package that compiles tenant-uploaded packet
filter rules into a small, verifiable data-plane instruction set. A program
is returned only after a static verifier proves that **every reachable packet
read lies inside a length that was established on every path reaching it**.

## Rule language

Rules are boolean predicates over one packet:

```
or      := and (OR and)*
and     := unary (AND unary)*
unary   := NOT unary | primary
primary := '(' or ')' | comparison
comparison := numeric CMP numeric
numeric := NUMBER | len | load
load    := ('u8'|'u16'|'u32') '(' NUMBER ')'
```

- Connectives: `and`/`&&`, `or`/`||`, `not`/`!` (AND/OR short-circuit).
- Comparisons: `== != < <= > >=`, all unsigned.
- `len` is the runtime packet length in bytes.
- `u8(off)`, `u16(off)`, `u32(off)` are big-endian fixed-width, fixed-offset
  reads of 1, 2 or 4 bytes.

Example:

```
len >= 8 and u16(0) == 4660 and u8(2) == 7
```

## Instruction set

The machine has two 32-bit registers (`R0` working, `R1` scratch) and a
strictly forward control-flow graph (no loops):

| Opcode  | Meaning                                              |
|---------|------------------------------------------------------|
| `IMM`   | load an immediate constant into a register           |
| `LEN`   | load the packet length into a register               |
| `LOAD`  | fixed-width read at a fixed offset into a register   |
| `JMP`   | unconditional forward jump                           |
| `JMPIF` | forward jump when `R0 <op> Imm` or `R0 <op> R1`      |
| `RET`   | terminate with a boolean verdict                     |

Only the four required capability classes are emitted: immediates, packet
length probes, fixed-length loads and forward jumps.

## Verification guarantee

`Compile` runs a monotone dataflow analysis over the forward CFG:

- Each program point carries `minLen`, a lower bound on packet length proven
  on **every** path reaching that point.
- A conditional edge derived from a length comparison refines the bound,
  e.g. `len >= k` proves `minLen >= k` on the taken edge and `len > k` proves
  `minLen >= k+1`.
- At a confluence the joined bound is the **minimum** of the incoming bounds
  (the common guarantee); divergent register identities collapse to unknown.
- A `LOAD` of `s` bytes at offset `o` is admitted only when
  `minLen >= o+s`; otherwise compilation fails.

Rejections are returned as `*RejectError`, which carries the originating
`Source` (expression id, source text, line and column), the instruction
index, the bytes required and the bytes actually guaranteed, so a tenant can
be pointed at the exact clause that made the rule unprovable.

## Differential testing

The semantics are implemented twice, independently:

- `reference.go` evaluates the syntax tree directly (the oracle).
- `vm.go` is a straightforward bytecode interpreter for compiled programs.

The test suite compares the two over:

- literally every packet of length 0..2 over all 256 byte values (65,793
  packets) — the short packets that fail every length guard;
- boundary-value exhaustive enumeration over larger packets (lengths up to
  the widest read), including all comparison operators and operand orders;
- seeded randomized fuzzing across packet lengths 0..19.

Any divergence fails the build. Verifier tests additionally cover the
join-takes-minimum rule, short-circuit guards, guards not leaking across OR
branches, overflow offsets and forward-only jumps.

## Usage

```go
import "example.org/packetvm"

rule, err := packetvm.Parse("allow", "len >= 8 and u32(0) == 1")
if err != nil { /* tenant syntax error */ }

prog, err := packetvm.Compile(rule)
if err != nil { /* *packetvm.RejectError: rule cannot be proven safe */ }

verdict, err := prog.Execute(pkt)
```

Run the tests with:

```
go test ./...
```
