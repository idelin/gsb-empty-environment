# CSE Optimizer for a Small Language

TypeScript implementation of a common-subexpression-elimination (CSE) optimizer
for a small straight-line SSA language with pure arithmetic, memory
loads/stores, and potentially-throwing instructions — plus an interpreter and
a differential test harness.

## Language

Each instruction defines a fresh temp (SSA); a program is a straight-line
instruction list ending in `ret`:

| Instruction        | Effect                                              | Throws            |
|--------------------|-----------------------------------------------------|-------------------|
| `t = const v`      | constant                                            | never             |
| `t = input i`      | read input vector                                   | never             |
| `t = add/sub/... a, b` | pure 32-bit arithmetic                          | never             |
| `t = div/mod a, b` | truncating division / remainder                     | zero divisor      |
| `t = load a`       | read `memory[a]`                                    | address out of bounds |
| `store a, v`       | write `memory[a] = v`                               | address out of bounds |
| `ret t`            | return value                                        | never             |

Observable behavior of a run = **return value + ordered write trace + first
exception** (`src/interp.ts`).

## Why text matching is not enough (`src/cse.ts`)

Two legality hazards make naive CSE unsound:

1. **Aliasing stores.** A `load` may only fold into an earlier load/store
   result for the same address temp if *no store executed in between*.
   Addresses are runtime values, so any store may alias any address: a store
   conservatively kills every cached load fact. Store-to-load forwarding
   (`store a, v` then `load a` → `v`) is kept only while no other store
   intervenes.
2. **Exception order.** Instructions are never reordered, and the *first*
   occurrence of a potentially-throwing computation is never removed. A later
   duplicate `div a, b` / `load a` folds into the earlier one only because the
   earlier instruction strictly precedes it: if the earlier one did not throw,
   the identical later one cannot throw either, so the first-exception
   behavior is preserved exactly.

Because temps are SSA, expressions are never invalidated by redefinition —
only stores invalidate (load facts).

## Differential testing (`src/difftest.ts`)

Seeded random programs (with deliberately cloned instructions to create CSE
opportunities, small constants so addresses alias and divisors hit zero) are
run under bounded random inputs. Original and optimized programs must agree on
return value, write trace, and first exception.

```
node dist/difftest.js [iterations]
```

Latest run: 100,000 programs / 300,000 executions (70.7% raise an exception,
covering all four error kinds), 225,406 instructions eliminated, 0 mismatches.

The harness was validated by mutation: an optimizer variant that does not kill
load facts on stores is caught immediately (mismatch: first exception changes,
e.g. `LoadOutOfBounds` vs `ModByZero`), as is a variant that keeps the later
of two duplicate divisions.

## Layout

- `src/lang.ts` — IR types and pretty printer
- `src/interp.ts` — interpreter producing `{ ret, writes, error }`
- `src/cse.ts` — the optimizer
- `src/gen.ts` — seeded random program/input generator
- `src/unittests.ts` — targeted tests for aliasing and exception ordering
- `src/difftest.ts` — differential test driver

## Build & test

```
npm install --save-dev typescript   # or any tsc >= 5
npm run build
npm test                            # unit tests + differential test
```
