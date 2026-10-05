import type { JsonValue, QueryResult } from "../src/index.ts";
import { encodePath } from "../src/index.ts";

export const store: JsonValue = {
  store: {
    book: [
      {
        category: "reference",
        author: "Nigel Rees",
        title: "Sayings of the Century",
        price: 8.95,
      },
      {
        category: "fiction",
        author: "Evelyn Waugh",
        title: "Sword of Honour",
        price: 12.99,
      },
      {
        category: "fiction",
        author: "Herman Melville",
        title: "Moby Dick",
        isbn: "0-553-21311-3",
        price: 8.99,
      },
      {
        category: "fiction",
        author: "J. R. R. Tolkien",
        title: "The Lord of the Rings",
        isbn: "0-395-19395-8",
        price: 22.99,
      },
    ],
    bicycle: { color: "red", price: 19.95 },
  },
  expensive: 10,
};

export function sorted(results: QueryResult[]): Array<{ key: string; value: string }> {
  return results
    .map((r) => ({ key: encodePath(r.path), value: JSON.stringify(r.value) }))
    .sort((a, b) => (a.key < b.key ? -1 : a.key > b.key ? 1 : a.value < b.value ? -1 : 1));
}

export function pathsOf(results: QueryResult[]): string[] {
  return results.map((r) => encodePath(r.path));
}

/** Deterministic PRNG (mulberry32) for reproducible random tests. */
export function mulberry32(seed: number): () => number {
  let a = seed;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const KEY_POOL = ["a", "b", "c", "a/b", "arr", "nested", "leaf", "x", "n", "tilde~key"];

export function randomTree(rand: () => number, depth = 0): JsonValue {
  const r = rand();
  if (depth > 4 || r < 0.3) {
    const s = rand();
    if (s < 0.25) return Math.floor(rand() * 10);
    if (s < 0.5) return `v${Math.floor(rand() * 5)}`;
    if (s < 0.7) return rand() < 0.5;
    return null;
  }
  if (r < 0.65) {
    const obj: { [key: string]: JsonValue } = {};
    const count = Math.floor(rand() * 4);
    for (let i = 0; i < count; i++) {
      const key = KEY_POOL[Math.floor(rand() * KEY_POOL.length)];
      obj[key] = randomTree(rand, depth + 1);
    }
    if (rand() < 0.4) obj.n = Math.floor(rand() * 6);
    if (rand() < 0.3) obj.x = Math.floor(rand() * 3);
    return obj;
  }
  const arr: JsonValue[] = [];
  const count = Math.floor(rand() * 5);
  for (let i = 0; i < count; i++) arr.push(randomTree(rand, depth + 1));
  return arr;
}

/** Builds a deeply nested tree {a: {a: {a: ... : 1}}} of the given depth. */
export function deepTree(depth: number): JsonValue {
  let value: JsonValue = 1;
  for (let i = 0; i < depth; i++) value = { a: value };
  return value;
}
