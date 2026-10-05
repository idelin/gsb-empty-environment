import type { BundlerOptions } from "./types.js";

export interface AliasRule {
  raw: string;
  from: string[];
  to: string[];
}

export interface CanonicalRules {
  aliases: AliasRule[];
  aliasesInverse: AliasRule[];
  extensions: string[];
  caseSensitive: boolean;
  resolveIndexFiles: boolean;
}

export const DEFAULT_EXTENSIONS: string[] = [".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".json"];

export function splitSegments(path: string): string[] {
  const unified = path.split("\\").join("/");
  const raw = unified.split("/");
  const out: string[] = [];
  for (const seg of raw) {
    if (seg.length > 0) {
      out.push(seg);
    }
  }
  return out;
}

export function normalizeSegments(segs: string[]): string[] {
  const out: string[] = [];
  for (const seg of segs) {
    if (seg === ".") {
      continue;
    }
    if (seg === "..") {
      if (out.length > 0 && out[out.length - 1] !== "..") {
        out.pop();
      } else {
        out.push("..");
      }
      continue;
    }
    out.push(seg);
  }
  return out;
}

export function segmentsStartWith(segs: string[], prefix: string[]): boolean {
  if (segs.length < prefix.length) {
    return false;
  }
  for (let i = 0; i < prefix.length; i++) {
    if (segs[i] !== prefix[i]) {
      return false;
    }
  }
  return true;
}

export function applyAlias(segs: string[], rules: CanonicalRules): { segs: string[]; alias: AliasRule | null } {
  for (const alias of rules.aliases) {
    if (segmentsStartWith(segs, alias.from)) {
      const rest = segs.slice(alias.from.length);
      return { segs: alias.to.concat(rest), alias };
    }
  }
  return { segs, alias: null };
}

export function stripKnownExtension(name: string, rules: CanonicalRules): string {
  const lower = name.toLowerCase();
  for (const ext of rules.extensions) {
    if (lower.length > ext.length && lower.endsWith(ext)) {
      return name.slice(0, name.length - ext.length);
    }
  }
  return name;
}

export function hasKnownExtension(name: string, rules: CanonicalRules): boolean {
  return stripKnownExtension(name, rules) !== name;
}

export function literalSegments(path: string, rules: CanonicalRules): string[] {
  const aliased = applyAlias(splitSegments(path), rules);
  return normalizeSegments(aliased.segs);
}

export function literalKey(path: string, rules: CanonicalRules): string {
  return literalSegments(path, rules).join("/");
}

export function dirSegmentsOf(path: string, rules: CanonicalRules): string[] {
  const segs = literalSegments(path, rules);
  return segs.slice(0, segs.length - 1);
}

function finishSegments(segs: string[], rules: CanonicalRules): string[] {
  let out = segs.slice();
  if (out.length > 0) {
    const lastIdx = out.length - 1;
    out[lastIdx] = stripKnownExtension(out[lastIdx], rules);
  }
  if (rules.resolveIndexFiles && out.length > 0 && out[out.length - 1].toLowerCase() === "index") {
    out = out.slice(0, out.length - 1);
  }
  if (!rules.caseSensitive) {
    out = out.map((s: string) => s.toLowerCase());
  }
  return out;
}

export function canonicalSegments(path: string, rules: CanonicalRules): string[] {
  return finishSegments(literalSegments(path, rules), rules);
}

export function canonicalKeyOfNormalized(segs: string[], rules: CanonicalRules): string {
  return finishSegments(segs, rules).join("/");
}

export function canonicalKey(path: string, rules: CanonicalRules): string {
  return canonicalSegments(path, rules).join("/");
}

function compareRaw(a: string, b: string): number {
  if (a < b) {
    return -1;
  }
  if (a > b) {
    return 1;
  }
  return 0;
}

export function makeRules(options?: BundlerOptions): CanonicalRules {
  const opts: BundlerOptions = options ?? {};
  const rawExtensions = opts.extensions ?? DEFAULT_EXTENSIONS;
  const extensions: string[] = [];
  for (const raw of rawExtensions) {
    const lower = raw.toLowerCase();
    if (lower.startsWith(".")) {
      extensions.push(lower);
    } else {
      extensions.push("." + lower);
    }
  }
  const aliases: AliasRule[] = [];
  const rawAliases = opts.aliases ?? {};
  for (const key of Object.keys(rawAliases)) {
    const from = normalizeSegments(splitSegments(key));
    const to = normalizeSegments(splitSegments(rawAliases[key]));
    if (from.length === 0 || to.length === 0) {
      throw new Error("invalid alias mapping: " + key);
    }
    if (from[0] === "." || from[0] === "..") {
      throw new Error("alias must not be relative: " + key);
    }
    aliases.push({ raw: key, from, to });
  }
  const byFromLength = (a: AliasRule, b: AliasRule) => {
    if (b.from.length !== a.from.length) {
      return b.from.length - a.from.length;
    }
    return compareRaw(a.raw, b.raw);
  };
  const byToLength = (a: AliasRule, b: AliasRule) => {
    if (b.to.length !== a.to.length) {
      return b.to.length - a.to.length;
    }
    return compareRaw(a.raw, b.raw);
  };
  aliases.sort(byFromLength);
  const aliasesInverse = aliases.slice().sort(byToLength);
  const caseSensitive = opts.caseSensitive ?? false;
  const resolveIndexFiles = opts.resolveIndexFiles ?? true;
  return { aliases, aliasesInverse, extensions, caseSensitive, resolveIndexFiles };
}
