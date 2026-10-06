# Bytecode Verifier

TypeScript verifier for a small stack VM. Programs are byte arrays with
variable-length instructions, so jump targets are byte offsets and must land
on instruction boundaries.

## Instruction set

| Opcode | Bytes | Stack effect | Requirement |
|--------|-------|--------------|-------------|
| `PUSH` | 1     | +1           | result <= maxStack |
| `POP`  | 1     | -1           | height >= 1 |
| `ADD`  | 1     | -1           | height >= 2 |
| `JMP t`| 3     | 0            | `t` is an instruction boundary |
| `JZ t` | 3     | -1           | height >= 1, `t` is a boundary; falls through or jumps |
| `RET`  | 1     | -            | height == 1 |

## Verification rules

- Every reachable instruction has exactly one entry stack height across all paths.
- No reachable path underflows or exceeds `maxStack`.
- `RET` executes only at height 1; control must not fall off the end.
- Jumps target instruction boundaries only.
- Unreachable instructions are never checked, so dead bad code is accepted.

The verifier (`src/verify.ts`) is a worklist dataflow over `(offset, height)`
states. Every error carries witness evidence: the full path from the program
entry with per-instruction entry heights, and two diverging paths for height
conflicts. `formatEvidence` renders them as readable traces.

## Validation

- `src/tests.ts` — directed tests for every error kind, witness-path contents,
  and unreachable-bad-code acceptance.
- `src/explore.ts` — exhaustive exploration of all programs of length 1-4 plus
  200k seeded random programs of length 5-8, cross-checked against an
  independent path-enumeration reference (`src/reference.ts`). Asserts zero
  disagreement and that accepted programs with unreachable bad code exist in
  every category (dead bad jumps, dead underflows, dead unconstrained RETs).

## Run

```sh
tsc -p tsconfig.json   # any TypeScript >= 4.x; no runtime dependencies
node dist/tests.js
node dist/explore.js
node dist/demo.js      # prints formatted witness evidence
```
