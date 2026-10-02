import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { SlidingWindowQuantile, type TimedEvent } from "../src/slidingWindowQuantile.ts";

const WINDOW_MS = 10_000;

function event(id: string, timestamp: number, value: number): TimedEvent {
  return { id, timestamp, value };
}

describe("acceptance: exact quantile with duplicates and retraction", () => {
  it("returns 1 for q=0.5 over [1, 1, 9, 20]", () => {
    const win = new SlidingWindowQuantile(WINDOW_MS);
    win.add(event("a", 1_000, 1));
    win.add(event("b", 2_000, 1));
    win.add(event("c", 3_000, 9));
    win.add(event("d", 4_000, 20));
    // floor(0.5 * (4 - 1)) = 1 -> sorted [1, 1, 9, 20][1] = 1
    assert.equal(win.quantile(0.5), 1);
  });

  it("returns 9 immediately after retracting one of the 1s", () => {
    const win = new SlidingWindowQuantile(WINDOW_MS);
    win.add(event("a", 1_000, 1));
    win.add(event("b", 2_000, 1));
    win.add(event("c", 3_000, 9));
    win.add(event("d", 4_000, 20));
    assert.equal(win.retract("a"), true);
    // floor(0.5 * (3 - 1)) = 1 -> sorted [1, 9, 20][1] = 9
    assert.equal(win.quantile(0.5), 9);
    assert.equal(win.size, 3);
  });

  it("retracting an unknown id is a no-op", () => {
    const win = new SlidingWindowQuantile(WINDOW_MS);
    win.add(event("a", 1_000, 1));
    assert.equal(win.retract("missing"), false);
    assert.equal(win.size, 1);
  });
});

describe("rank semantics: floor(q * (n - 1))", () => {
  it("maps q=0 to min and q=1 to max", () => {
    const win = new SlidingWindowQuantile(WINDOW_MS);
    [5, 1, 9, 3].forEach((v, i) => win.add(event(`e${i}`, i + 1, v)));
    assert.equal(win.quantile(0), 1);
    assert.equal(win.quantile(1), 9);
  });

  it("floors the rank instead of rounding", () => {
    const win = new SlidingWindowQuantile(WINDOW_MS);
    [10, 20, 30, 40, 50].forEach((v, i) => win.add(event(`e${i}`, i + 1, v)));
    // floor(0.5 * 4) = 2 -> 30; floor(0.4 * 4) = 1 -> 20; floor(0.99 * 4) = 3 -> 40
    assert.equal(win.quantile(0.5), 30);
    assert.equal(win.quantile(0.4), 20);
    assert.equal(win.quantile(0.99), 40);
  });

  it("rejects q outside [0, 1] and empty windows", () => {
    const win = new SlidingWindowQuantile(WINDOW_MS);
    assert.throws(() => win.quantile(0.5), /empty/);
    win.add(event("a", 1, 1));
    assert.throws(() => win.quantile(-0.1), RangeError);
    assert.throws(() => win.quantile(1.1), RangeError);
  });
});

describe("sliding window expiry (window = 10s)", () => {
  it("expires events strictly older than now - windowMs", () => {
    const win = new SlidingWindowQuantile(WINDOW_MS);
    win.add(event("old", 0, 100));
    win.add(event("mid", 5_000, 50));
    win.add(event("new", 10_000, 1)); // advances now to 10s
    // cutoff = 10_000 - 10_000 = 0 -> event at t=0 expires
    assert.equal(win.size, 2);
    assert.deepEqual([win.quantile(0), win.quantile(1)], [1, 50]);
  });

  it("keeps boundary events at exactly now - windowMs + epsilon and drops equality", () => {
    const win = new SlidingWindowQuantile(WINDOW_MS);
    win.add(event("boundary", 1, 7)); // t = 1
    win.add(event("inside", 2, 8)); // t = 2
    win.advance(10_001); // cutoff = 1 -> t=1 expires (half-open window)
    assert.equal(win.size, 1);
    assert.equal(win.quantile(0), 8);
    win.advance(10_002); // cutoff = 2 -> t=2 expires too
    assert.equal(win.size, 0);
  });

  it("expires continuously while data flows in", () => {
    const win = new SlidingWindowQuantile(WINDOW_MS);
    for (let t = 0; t <= 30_000; t += 1_000) {
      win.add(event(`e${t}`, t, t / 1_000));
      if (t >= 10_000) {
        // window (t-10s, t] holds exactly 10 events
        assert.equal(win.size, 10, `at t=${t}`);
        assert.equal(win.quantile(0), t / 1_000 - 9);
        assert.equal(win.quantile(1), t / 1_000);
      }
    }
  });

  it("advance is monotonic and ignores moves backwards", () => {
    const win = new SlidingWindowQuantile(WINDOW_MS);
    win.add(event("a", 20_000, 1));
    win.advance(10_000);
    assert.equal(win.size, 1);
    assert.equal(win.currentTime, 20_000);
  });
});

describe("out-of-order insertion", () => {
  it("accepts late events that are still inside the window", () => {
    const win = new SlidingWindowQuantile(WINDOW_MS);
    win.add(event("now", 20_000, 100));
    win.add(event("late", 15_000, 5)); // out of order, still live
    assert.equal(win.size, 2);
    assert.equal(win.quantile(0), 5);
    assert.equal(win.quantile(1), 100);
  });

  it("rejects events that are already expired on arrival", () => {
    const win = new SlidingWindowQuantile(WINDOW_MS);
    win.add(event("now", 20_000, 100));
    assert.equal(win.add(event("stale", 9_999, 1)), false);
    assert.equal(win.size, 1);
    assert.equal(win.quantile(0.5), 100);
  });
});

describe("boundary-equal events", () => {
  it("treats equal (timestamp, value) events as distinct and retracts exactly one", () => {
    const win = new SlidingWindowQuantile(WINDOW_MS);
    win.add(event("x", 5_000, 42));
    win.add(event("y", 5_000, 42));
    assert.equal(win.size, 2);
    assert.equal(win.retract("x"), true);
    assert.equal(win.size, 1);
    assert.equal(win.quantile(0.5), 42);
    assert.equal(win.retract("y"), true);
    assert.equal(win.size, 0);
  });

  it("expires same-timestamp boundary events together", () => {
    const win = new SlidingWindowQuantile(WINDOW_MS);
    win.add(event("x", 1_000, 1));
    win.add(event("y", 1_000, 2));
    win.add(event("z", 1_001, 3));
    win.advance(11_000); // cutoff = 1_000 -> x and y expire, z survives
    assert.equal(win.size, 1);
    assert.equal(win.quantile(0.5), 3);
  });

  it("rejects duplicate ids", () => {
    const win = new SlidingWindowQuantile(WINDOW_MS);
    win.add(event("a", 1, 1));
    assert.throws(() => win.add(event("a", 2, 2)), /duplicate/);
  });
});

describe("randomized cross-check against a sorted-array oracle", () => {
  // Deterministic PRNG so failures reproduce.
  function mulberry32(seed: number): () => number {
    let state = seed;
    return () => {
      state |= 0;
      state = (state + 0x6d2b79f5) | 0;
      let t = Math.imul(state ^ (state >>> 15), 1 | state);
      t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
  }

  it("matches the oracle across mixed add/retract/advance workloads", () => {
    const rand = mulberry32(0x5eed);
    const win = new SlidingWindowQuantile(WINDOW_MS);
    const oracle = new Map<string, TimedEvent>();
    let now = 0;
    let nextId = 0;

    const expectedQuantile = (q: number): number => {
      const values = [...oracle.values()].map((e) => e.value).sort((a, b) => a - b);
      return values[Math.floor(q * (values.length - 1))];
    };

    for (let step = 0; step < 5_000; step++) {
      const roll = rand();
      if (roll < 0.55) {
        // Mostly in-order, sometimes up to 15s late (out of order / stale).
        const jitter = Math.floor(rand() * 16_000);
        const ts = now + (rand() < 0.7 ? 0 : -jitter);
        const e = event(`e${nextId++}`, ts, Math.floor(rand() * 1_000));
        const accepted = win.add(e);
        const shouldAccept = ts > now - WINDOW_MS;
        assert.equal(accepted, shouldAccept, `add(${e.id}@${ts}) at now=${now}`);
        if (accepted) oracle.set(e.id, e);
      } else if (roll < 0.75 && oracle.size > 0) {
        const ids = [...oracle.keys()];
        const id = ids[Math.floor(rand() * ids.length)];
        assert.equal(win.retract(id), true);
        oracle.delete(id);
      } else {
        now += Math.floor(rand() * 3_000);
        win.advance(now);
        for (const [id, e] of oracle) {
          if (e.timestamp <= now - WINDOW_MS) oracle.delete(id);
        }
      }
      assert.equal(win.size, oracle.size, `size at step ${step}`);
      if (oracle.size > 0) {
        for (const q of [0, 0.25, 0.5, 0.75, 0.9, 1]) {
          assert.equal(
            win.quantile(q),
            expectedQuantile(q),
            `q=${q} at step ${step}`,
          );
        }
      }
    }
  });
});
