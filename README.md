# rational-timecode

Rational-frame-rate timecode toolkit in pure TypeScript on BigInt. Frame
numbers, timecode labels, and audio sample positions are converted with exact
rational arithmetic — no floating-point accumulation, no `Number` detour for
big magnitudes, and every rounding is explicit.

Requires Node.js >= 23.6 (runs `.ts` directly via type stripping; no build
step, no dependencies).

```sh
npm test    # node --test test/*.test.ts
npm run demo
```

## Concepts

**Rational frame rate.** A rate is `num/den` frames per second (`30000/1001`
for 29.97, `24000/1001` for 23.976). The nominal integer rate used in labels
is `round(num/den)` (ties up): `30000/1001 -> 30`.

**Drop-frame is a label rule, never lost frames.** A `DropFrameRule` is an
explicit period table: `periodFrames` nominal label positions per period plus
the sorted list of `drops` — label positions that do not exist. Real frames
stay densely numbered `0, 1, 2, ...`; each frame simply carries the next
existing label. `ntscDropFrame(30)` builds the classic table: period 18000
(10 minutes at nominal 30), drops `00`/`01` of every minute except every 10th
(18 drops, 17982 valid labels per period). Arbitrary custom tables work the
same way.

**Bidirectional rank index.** `compileRankIndex(rule)` compiles the table
into `rankToNominal` / `nominalToRank` / `isValidNominal` /
`nearestValidNominal`. Conversion jumps whole periods with floored division
and binary-searches inside one period — O(log period) for any magnitude, no
per-frame scanning, exact for negative positions too.

**Labels.** Grammar `[-]HH:MM:SS:FF` (`;` before FF allowed only with a
drop-frame rule). Length policy: `HH` is 2+ digits and grows without bound
(no 24h wrap), `MM`/`SS`/`FF` exactly 2 digits, `FF < nominal fps` (2..99).
Nonexistent (dropped) labels are rejected with the nearest legal candidate —
ties resolve to the earlier label. Malformed labels are rejected with a
reason.

**Negative time.** Policy is explicit per `Timecode`: `'reject'` (default) or
`'signed'`, where `frameToLabel(-f) === '-' + frameToLabel(f)` and the
negative-zero label is invalid.

**Samples and splicing.** `frameToSamples` / `samplesToFrame` keep the exact
rational position and return the rounding residual as a rational `error`.
`Timeline` splices segments with different rates and rule versions;
`boundarySamples` are exact rationals, `boundarySamplesAligned` rounds them
with an explicit mode and reports rational residuals, and `locateBySamples`
aligns an audio sample offset to a segment-local frame with the residual
misalignment as a rational number of samples.

**Rounding modes** (all explicit, required at each call): `floor`, `ceil`,
`trunc`, `nearest-half-up` (ties toward +inf), `nearest-half-away` (ties away
from zero), `nearest-half-even`.

## Demo output (`npm run demo`)

```
== drop-frame period table (NTSC 29.97, nominal 30) ==
  rule id                                      ntsc-df-30
  period (nominal frames)                      18000
  dropped labels per period                    18
  valid labels per period                      17982
  first drops                                  1800, 1801, 3600, 3601

== frame <-> label (minute boundary skips labels, not frames) ==
  frame 1799                                   00:00:59;29
  frame 1800 (no frames lost)                  00:01:00;02
  frame 17982 (10-min boundary)                00:10:00;00
  frame 107892 (1 hour)                        01:00:00;00
  label 00:01:00;02 -> frame                   1800

== nonexistent label -> nearest legal candidate ==
  parse 00:01:00;00 ok?                        false
  kind                                         dropped-label
  nearest label                                00:00:59;29
  nearest frame                                1799

== audio sample alignment at 48 kHz (exact rational, explicit rounding) ==
  1 frame exact samples                        8008/5 = 1601.6000
  rounded samples                              1602
  rounding error (rational)                    2/5
  samples -> frame round trip                  1

== cross-rule splice: 29.97 DF segment + 24 NDF segment ==
  total frames                                 2800
  boundary samples (exact)                     0 -> 2882880 -> 4882880
  label at global frame 1800                   00:00:00:00
  sample 2884879 -> segment                    body
  sample 2884879 -> local frame / label        1 / 00:00:00:01
  alignment error (rational samples)           -1

== period jumps: huge and negative positions, no scanning ==
  rank 10^15 -> nominal                        1001001001001000
  round trip ok?                               true
  frame 10^15 label                            9268527787:02:46;20
  frame -1800 (signed policy)                  -00:01:00;02
  label -00:01:00;02 -> frame                  -1800
```

## Test results (`npm test`, Node v23.9.0)

37 tests, all passing. Coverage includes exhaustive round-trips (two full
periods in both directions, first 20000 frames, +/-64 frames around each of
the first 20 period boundaries), minute-boundary skip behavior, nearest legal
candidates, negative-time policy, exact rational sample alignment, cross-rule
timeline splicing, and 10^18-scale frames via period jumps:

```
ℹ tests 37
ℹ suites 0
ℹ pass 37
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
ℹ duration_ms 633.807542
```

## Layout

- `src/rational.ts` — BigInt rational arithmetic, explicit rounding modes
- `src/rate.ts` — rational frame rates, nominal fps
- `src/dropframe.ts` — period-table rules, compiled bidirectional rank index
- `src/timecode.ts` — label formatting/parsing, nearest-candidate, negative policy
- `src/samples.ts` — frame <-> sample conversion with rational residuals
- `src/timeline.ts` — cross-rule segment splicing and audio alignment
- `test/` — node:test suites; `examples/demo.ts` — runnable walkthrough
