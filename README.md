# Sliding-Window Conditional Duration Integrator

Computes the cumulative milliseconds within a sliding time window during which
a streaming metric satisfies a boolean condition (e.g. `temperature > 80`).

## How it works

- The metric stream is modeled as a **step function**: each sample holds its
  value until the next sample arrives.
- Only **state transitions** (condition false→true / true→false) are stored,
  not every sample.
- `totalActiveMs(now)` intersects the active intervals with the half-open
  window `[now - windowMs, now)` using exact integer-millisecond arithmetic.
- As the window slides past a transition point, its contribution is deducted
  exactly — no discrete numerical integration, hence **no accumulated drift**.
- Transitions older than the window are pruned lazily, keeping the last one
  before the window's left edge (it defines the state at that edge).

## Requirements

Node.js >= 22.18 (runs TypeScript directly via type stripping; no build step).

## Usage

```ts
import { SlidingWindowDurationIntegrator } from "./src/SlidingWindowDurationIntegrator.ts";

const integ = new SlidingWindowDurationIntegrator(100_000, (t) => t > 80);
integ.addSample(10_000, 85); // alarm on at t=10s
integ.addSample(40_000, 75); // alarm off at t=40s
integ.totalActiveMs(50_000); // 30000
integ.totalActiveMs(120_000); // 20000 (t=10s slid out of the 100s window)
integ.totalActiveMs(140_000); // 0
```

## Run

```sh
npm test    # node --test (Node built-in test runner)
npm run demo
```

## Demo output

```
t (s) | over-temp ms in last 100 s
------+---------------------------
   50 | 30000
  110 | 30000
  111 | 29000
  120 | 20000
  130 | 10000
  139 | 1000
  140 | 0
  150 | 0
```
