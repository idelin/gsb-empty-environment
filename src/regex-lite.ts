import { randInt, pick, type Rng } from "./rng.ts";

/**
 * A deliberately small regex *generation* subset (validation of `pattern`
 * always uses the real RegExp engine; this sampler is only for building
 * matching strings). Supported: anchored ^...$, literal chars, escaped
 * literals, \d \w \s, ".", [..] classes with ranges and negation,
 * quantifiers * + ? {n} {n,m} {n,}, and groups of literal alternatives
 * like (cat|dog). Anything else parses to null and the caller must
 * report "cannot construct" instead of guessing.
 */
export interface PatternTarget {
  min: number;
  max: number;
}

type Atom =
  | { kind: "char"; chars: string }
  | { kind: "alts"; options: string[] };

export interface Piece {
  atom: Atom;
  min: number;
  max: number; // may be Infinity
}

const UNIVERSE = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789";
const GROW_CAP = 40;

export function parsePattern(pattern: string): Piece[] | null {
  if (pattern.length < 2 || !pattern.startsWith("^") || !pattern.endsWith("$")) return null;
  const body = pattern.slice(1, -1);
  const pieces: Piece[] = [];
  let i = 0;

  const readAtom = (): Atom | null => {
    const c = body[i];
    if (c === "\\") {
      i++;
      if (i >= body.length) return null;
      const e = body[i++];
      if (e === "d") return { kind: "char", chars: "0123456789" };
      if (e === "w") return { kind: "char", chars: UNIVERSE + "_" };
      if (e === "s") return { kind: "char", chars: " \t" };
      if ("bBWS".includes(e)) return null;
      return { kind: "char", chars: e };
    }
    if (c === ".") {
      i++;
      return { kind: "char", chars: UNIVERSE };
    }
    if (c === "[") {
      i++;
      let negate = false;
      if (body[i] === "^") {
        negate = true;
        i++;
      }
      let chars = "";
      let first = true;
      while (i < body.length && (body[i] !== "]" || first)) {
        first = false;
        if (body[i] === "\\") {
          i++;
          if (i >= body.length) return null;
        }
        const lo = body[i++];
        if (body[i] === "-" && i + 1 < body.length && body[i + 1] !== "]") {
          i++;
          const hi = body[i++];
          if (lo > hi) return null;
          for (let code = lo.charCodeAt(0); code <= hi.charCodeAt(0); code++)
            chars += String.fromCharCode(code);
        } else {
          chars += lo;
        }
      }
      if (body[i] !== "]") return null;
      i++;
      if (negate) {
        const excluded = new Set(chars);
        chars = [...UNIVERSE].filter((ch) => !excluded.has(ch)).join("");
      }
      return chars ? { kind: "char", chars } : null;
    }
    if (c === "(") {
      i++;
      const options: string[] = [];
      let current = "";
      while (i < body.length && body[i] !== ")") {
        const ch = body[i];
        if (ch === "|") {
          options.push(current);
          current = "";
          i++;
          continue;
        }
        if ("\\[](){}*+?^$.".includes(ch)) return null;
        current += ch;
        i++;
      }
      if (body[i] !== ")") return null;
      i++;
      options.push(current);
      if (options.some((o) => o.length === 0)) return null;
      return { kind: "alts", options };
    }
    if ("*+?{}|)".includes(c)) return null;
    i++;
    return { kind: "char", chars: c };
  };

  while (i < body.length) {
    const atom = readAtom();
    if (!atom) return null;
    let min = 1;
    let max = 1;
    const q = body[i];
    if (q === "*") {
      min = 0;
      max = Infinity;
      i++;
    } else if (q === "+") {
      min = 1;
      max = Infinity;
      i++;
    } else if (q === "?") {
      min = 0;
      max = 1;
      i++;
    } else if (q === "{") {
      const close = body.indexOf("}", i);
      if (close === -1) return null;
      const spec = body.slice(i + 1, close);
      const m = /^(\d+)(?:,(\d*))?$/.exec(spec);
      if (!m) return null;
      min = Number(m[1]);
      max = m[2] === undefined ? (spec.includes(",") ? Infinity : min) : Number(m[2]);
      if (max < min) return null;
      i = close + 1;
    }
    pieces.push({ atom, min, max });
  }
  return pieces;
}

function minAtomLen(atom: Atom): number {
  if (atom.kind === "char") return 1;
  return Math.min(...atom.options.map((o) => o.length));
}

function renderAtom(atom: Atom, rng: Rng): string {
  if (atom.kind === "char") return atom.chars[randInt(rng, 0, atom.chars.length - 1)];
  return pick(rng, atom.options);
}

export function samplePattern(pieces: Piece[], rng: Rng, target?: PatternTarget): string {
  const reps: number[] = pieces.map((p) =>
    target || p.max === p.min ? p.min : randInt(rng, p.min, Math.min(p.max, p.min + 3)),
  );
  if (target) {
    let total = pieces.reduce((sum, p, idx) => sum + reps[idx] * minAtomLen(p.atom), 0);
    let guard = 0;
    while (total < target.min && guard++ < 300) {
      const growable = pieces
        .map((p, idx) => ({ p, idx }))
        .filter(({ p, idx }) => reps[idx] < Math.min(p.max, p.min + GROW_CAP))
        .map(({ idx }) => idx);
      if (growable.length === 0) break;
      const idx = pick(rng, growable);
      reps[idx]++;
      total += minAtomLen(pieces[idx].atom);
    }
  }
  let out = "";
  pieces.forEach((p, idx) => {
    for (let r = 0; r < reps[idx]; r++) out += renderAtom(p.atom, rng);
  });
  return out;
}
