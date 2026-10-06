import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { ChatListLayout } from "../src/layout.ts";
import { ChatViewport } from "../src/viewport.ts";
import { NaiveLayout, NaiveViewport } from "./naive.ts";

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

function makeRandom(seed: number) {
  const next = mulberry32(seed);
  return {
    int: (lo: number, hi: number) => lo + Math.floor(next() * (hi - lo + 1)),
    pick: <T>(values: readonly T[]): T => values[Math.floor(next() * values.length)]!,
    chance: (p: number) => next() < p,
  };
}

const ESTIMATED = 50;
const VIEWPORT_HEIGHT = 320;

function randomItems(rand: ReturnType<typeof makeRandom>, count: number, keys: { next: number }) {
  return Array.from({ length: count }, () => {
    const key = `k${keys.next++}`;
    // Most items start unmeasured; some arrive with a measured height.
    return rand.chance(0.3) ? { key, height: rand.int(0, 300) } : { key };
  });
}

function existingKeys(layout: ChatListLayout): string[] {
  return Array.from({ length: layout.count }, (_, i) => layout.keyAt(i));
}

function assertLayoutsEqual(actual: ChatListLayout, expected: NaiveLayout, round: number) {
  const label = `round ${round}`;
  assert.equal(actual.count, expected.count, `${label}: count`);
  assert.equal(actual.totalHeight(), expected.totalHeight(), `${label}: totalHeight`);
  for (let i = 0; i < expected.count; i++) {
    assert.equal(actual.keyAt(i), expected.keyAt(i), `${label}: keyAt(${i})`);
    assert.equal(actual.heightOf(i), expected.heightOf(i), `${label}: heightOf(${i})`);
    assert.equal(actual.topOf(i), expected.topOf(i), `${label}: topOf(${i})`);
  }
  assert.equal(actual.topOf(expected.count), expected.topOf(expected.count), `${label}: topOf(count)`);
}

function assertLocationsEqual(
  actual: ChatListLayout,
  expected: NaiveLayout,
  offsets: readonly number[],
  round: number,
) {
  for (const offset of offsets) {
    const a = actual.locate(offset);
    const e = expected.locate(offset);
    const label = `round ${round}, locate(${offset})`;
    if (e === null) {
      assert.equal(a, null, label);
      continue;
    }
    assert.equal(a!.key, e.key, `${label}: key`);
    assert.equal(a!.top, e.top, `${label}: top`);
    assert.equal(a!.offsetInItem, e.offsetInItem, `${label}: offsetInItem`);
  }
}

/** Probe offsets covering boundaries: edges, exact prefix sums, and beyond. */
function probeOffsets(rand: ReturnType<typeof makeRandom>, model: NaiveLayout): number[] {
  const total = model.totalHeight();
  const probes = [-1, 0, 1, total - 1, total, total + 1, total + 500];
  for (const prefix of model.prefixes()) {
    if (rand.chance(0.3)) {
      probes.push(prefix); // exact item boundary
    }
  }
  for (let i = 0; i < 5; i++) {
    probes.push(rand.int(-50, total + 50));
  }
  return probes;
}

describe("differential: continuous batch updates vs full prefix-sum model", () => {
  for (const seed of [1, 7, 42, 1337]) {
    it(`matches the naive model across 200 rounds (seed ${seed})`, () => {
      const rand = makeRandom(seed);
      const layout = new ChatListLayout({ estimatedHeight: ESTIMATED });
      const naive = new NaiveLayout({ estimatedHeight: ESTIMATED });
      const viewport = new ChatViewport(layout, { viewportHeight: VIEWPORT_HEIGHT });
      const naiveViewport = new NaiveViewport(naive, { viewportHeight: VIEWPORT_HEIGHT });
      const keys = { next: 0 };

      for (let round = 0; round < 200; round++) {
        const batchSize = rand.int(1, 6);
        for (let op = 0; op < batchSize; op++) {
          const kind = rand.pick(["prepend", "append", "insert", "remove", "measure", "scroll"] as const);
          switch (kind) {
            case "prepend": {
              const items = randomItems(rand, rand.int(1, 5), keys);
              viewport.prepend(items);
              naiveViewport.prepend(items);
              break;
            }
            case "append": {
              const items = randomItems(rand, rand.int(1, 5), keys);
              viewport.append(items);
              naiveViewport.append(items);
              break;
            }
            case "insert": {
              if (layout.count === 0) break;
              const items = randomItems(rand, rand.int(1, 3), keys);
              const index = rand.int(0, layout.count);
              viewport.insert(index, items);
              naiveViewport.insert(index, items);
              break;
            }
            case "remove": {
              const candidates = existingKeys(layout);
              if (candidates.length === 0) break;
              const removeCount = rand.int(1, Math.min(4, candidates.length));
              const doomed = new Set<string>();
              while (doomed.size < removeCount) {
                doomed.add(rand.pick(candidates));
              }
              const removed = [...doomed];
              viewport.removeKeys(removed);
              naiveViewport.removeKeys(removed);
              break;
            }
            case "measure": {
              const candidates = existingKeys(layout);
              if (candidates.length === 0) break;
              const key = rand.pick(candidates);
              const height = rand.int(0, 300);
              viewport.measure(key, height);
              naiveViewport.measure(key, height);
              break;
            }
            case "scroll": {
              // Boundary-biased scrolling: edges, exact tops, and out of range.
              const total = layout.totalHeight();
              const target = rand.chance(0.4)
                ? rand.pick([-100, -1, 0, total, total + 1, total + 1000])
                : rand.int(-50, total + 50);
              viewport.scrollTo(target);
              naiveViewport.scrollTo(target);
              break;
            }
          }
        }

        assertLayoutsEqual(layout, naive, round);
        assertLocationsEqual(layout, naive, probeOffsets(rand, naive), round);
        assert.equal(viewport.maxOffset, naiveViewport.maxOffset, `round ${round}: maxOffset`);
        assert.equal(
          viewport.scrollOffset,
          naiveViewport.scrollOffset,
          `round ${round}: scrollOffset`,
        );
      }
    });
  }

  it("keeps the anchored position identical to the naive model while history streams in", () => {
    const rand = makeRandom(99);
    const layout = new ChatListLayout({ estimatedHeight: ESTIMATED });
    const naive = new NaiveLayout({ estimatedHeight: ESTIMATED });
    const viewport = new ChatViewport(layout, { viewportHeight: VIEWPORT_HEIGHT });
    const naiveViewport = new NaiveViewport(naive, { viewportHeight: VIEWPORT_HEIGHT });
    const keys = { next: 0 };

    const initial = randomItems(rand, 40, keys);
    viewport.append(initial);
    naiveViewport.append(initial);
    viewport.scrollTo(1234);
    naiveViewport.scrollTo(1234);

    for (let round = 0; round < 100; round++) {
      // Simulate a chat session: older history prepended in pages, fresh
      // messages appended, visible items measured on demand.
      const history = randomItems(rand, rand.int(1, 10), keys);
      viewport.prepend(history);
      naiveViewport.prepend(history);

      const fresh = randomItems(rand, rand.int(0, 3), keys);
      viewport.append(fresh);
      naiveViewport.append(fresh);

      const candidates = existingKeys(layout);
      for (let i = 0; i < 3 && candidates.length > 0; i++) {
        const key = rand.pick(candidates);
        const height = rand.int(0, 300);
        viewport.measure(key, height);
        naiveViewport.measure(key, height);
      }

      assertLayoutsEqual(layout, naive, round);
      assert.equal(viewport.scrollOffset, naiveViewport.scrollOffset, `round ${round}: scrollOffset`);

      // The first visible item and its inner offset stay put across the round.
      const location = layout.locate(viewport.scrollOffset);
      const naiveLocation = naive.locate(naiveViewport.scrollOffset);
      assert.equal(location?.key, naiveLocation?.key, `round ${round}: anchor key`);
      assert.equal(location?.offsetInItem, naiveLocation?.offsetInItem, `round ${round}: anchor offset`);
    }
  });
});
