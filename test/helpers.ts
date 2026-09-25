import { canonical, type JsonValue, type PathRecord } from "../src/record.ts";
import { formatPath, pathToArray, type Seg } from "../src/path.ts";

export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function k(...keys: string[]): Seg[] {
  return keys.map((key) => ({ t: "k", k: key }) as Seg);
}

export function recordLine(rec: PathRecord): string {
  return `${formatPath(pathToArray(rec.path))} ${canonical(rec)}`;
}

export function shuffled<T>(items: T[], rnd: () => number): T[] {
  const out = [...items];
  for (let i = out.length - 1; i > 0; i--) {
    const j = Math.floor(rnd() * (i + 1));
    [out[i], out[j]] = [out[j], out[i]];
  }
  return out;
}

const KEY_POOL = ["0", "1", "2", "10", "a", "b", "key", "", "x y", "__proto__", "constructor"];
const STR_POOL = ["", "x", "0", "01", "hello world", "键", "null", "[1]"];

/** Random JSON tree exercising null, empty containers, holes, numeric keys. */
export function randomTree(rnd: () => number, depth: number): JsonValue {
  const r = rnd();
  if (depth <= 0 || r < 0.45) {
    const s = rnd();
    if (s < 0.2) return null;
    if (s < 0.4) return rnd() < 0.5;
    if (s < 0.7) return Math.floor(rnd() * 2000) - 1000;
    return STR_POOL[Math.floor(rnd() * STR_POOL.length)];
  }
  if (r < 0.75) {
    const out: { [key: string]: JsonValue } = {};
    const n = Math.floor(rnd() * 5);
    for (let i = 0; i < n; i++) {
      const key = KEY_POOL[Math.floor(rnd() * KEY_POOL.length)];
      Object.defineProperty(out, key, {
        value: randomTree(rnd, depth - 1),
        enumerable: true,
        writable: true,
        configurable: true,
      });
    }
    return out;
  }
  const len = Math.floor(rnd() * 5);
  const arr: JsonValue[] = new Array(len);
  for (let i = 0; i < len; i++) {
    if (rnd() < 0.25) continue; // array hole
    arr[i] = randomTree(rnd, depth - 1);
  }
  return arr;
}
