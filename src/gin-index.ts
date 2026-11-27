import type { JsonPathAst, JsonValue } from './ast.ts';
import {
  canonicalScalar,
  extractIndexPlan,
  FULL_SCAN,
  type Conjunction,
  type IndexFact,
  type IndexPlan,
} from './index-plan.ts';

/**
 * In-memory emulation of a GIN jsonb_path_ops index.
 *
 * Each indexed document contributes:
 *  - one `key` fact per reachable object member (array positions are ignored),
 *  - one `leaf` fact per scalar leaf on a key path.
 * The posting lists map fact -> set of document ids, so conjunctions and
 * disjunctions reduce to set intersections/unions — exactly the operations
 * a GIN index supports natively.
 */
export class GinPathIndex {
  private readonly postings = new Map<string, Set<number>>();
  private readonly documentFacts = new Map<number, Set<string>>();
  private size = 0;

  addDocument(id: number, document: JsonValue): void {
    const facts = collectFacts(document);
    const encoded = new Set<string>();
    for (const fact of facts) {
      const key = encodeFact(fact);
      encoded.add(key);
      let posting = this.postings.get(key);
      if (posting === undefined) {
        posting = new Set<number>();
        this.postings.set(key, posting);
      }
      posting.add(id);
    }
    this.documentFacts.set(id, encoded);
    this.size = Math.max(this.size, id + 1);
  }

  get documentCount(): number {
    return this.size;
  }

  /**
   * Apply a pushdown plan and return the candidate document ids.
   * FULL_SCAN returns every id; otherwise the result is the OR of the
   * conjunction groups evaluated against the posting lists.
   */
  query(plan: IndexPlan): number[] {
    if (plan.fullScan) return [...this.documentFacts.keys()].sort((a, b) => a - b);

    const matched = new Set<number>();
    for (const group of plan.groups) {
      for (const id of this.evaluateConjunction(group)) matched.add(id);
    }
    return [...matched].sort((a, b) => a - b);
  }

  /** Convenience: parse (or reuse) an AST and pre-filter with its plan. */
  preFilter(ast: JsonPathAst): number[] {
    return this.query(extractIndexPlan(ast));
  }

  private evaluateConjunction(group: Conjunction): Set<number> {
    let candidates: Set<number> | null = null;

    for (const fact of group.exact) {
      const posting = this.postings.get(encodeFact(fact));
      if (posting === undefined) return new Set();
      candidates = candidates === null ? new Set(posting) : intersect(candidates, posting);
      if (candidates.size === 0) return new Set();
    }

    const hasSuffixConstraints =
      group.suffixKeyPaths.length > 0 ||
      group.suffixLeaves.length > 0 ||
      group.anchoredSuffixes.length > 0 ||
      group.anchoredLeaves.length > 0;
    if (!hasSuffixConstraints) {
      return candidates ?? new Set(this.documentFacts.keys());
    }

    // Suffix constraints cannot use a direct posting-list lookup; scan the
    // current candidate set for documents owning matching key paths/leaves.
    const pool = candidates === null ? [...this.documentFacts.keys()] : [...candidates];
    const result = new Set<number>();
    for (const id of pool) {
      const facts = this.documentFacts.get(id);
      if (facts === undefined) continue;
      const keysPass = group.suffixKeyPaths.every((suffix) =>
        this.hasKeyPathSuffix(facts, suffix),
      );
      const leavesPass = group.suffixLeaves.every((leaf) =>
        this.hasLeafSuffix(facts, leaf),
      );
      const anchoredPass = group.anchoredSuffixes.every((anchored) =>
        this.hasAnchoredSuffix(facts, anchored.prefix, anchored.suffix),
      );
      const anchoredLeavesPass = group.anchoredLeaves.every((leaf) =>
        this.hasAnchoredLeaf(facts, leaf.prefix, leaf.suffix, leaf.value),
      );
      if (keysPass && leavesPass && anchoredPass && anchoredLeavesPass) {
        result.add(id);
      }
    }
    return result;
  }

  private hasKeyPathSuffix(facts: Set<string>, suffix: readonly string[]): boolean {
    for (const encoded of facts) {
      if (!encoded.startsWith('k:')) continue;
      const keyPath = encoded.slice(2).split('.');
      if (endsWith(keyPath, suffix)) return true;
    }
    return false;
  }

  private hasLeafSuffix(
    facts: Set<string>,
    leaf: Extract<IndexFact, { kind: 'leaf' }>,
  ): boolean {
    const requiredValue = canonicalScalar(leaf.value);
    for (const encoded of facts) {
      if (!encoded.startsWith('v:')) continue;
      const separator = encoded.indexOf('=');
      const keyPath = encoded.slice(2, separator).split('.');
      const value = encoded.slice(separator + 1);
      if (value === requiredValue && endsWith(keyPath, leaf.keyPath)) return true;
    }
    return false;
  }

  /** True when some key path starts with `prefix` and ends with `suffix`. */
  private hasAnchoredSuffix(
    facts: Set<string>,
    prefix: readonly string[],
    suffix: readonly string[],
  ): boolean {
    for (const encoded of facts) {
      if (!encoded.startsWith('k:')) continue;
      const keyPath = encoded.slice(2).split('.');
      if (startsWith(keyPath, prefix) && hasContainedSuffix(keyPath, prefix, suffix)) {
        return true;
      }
    }
    return false;
  }

  private hasAnchoredLeaf(
    facts: Set<string>,
    prefix: readonly string[],
    suffix: readonly string[],
    value: string | number | boolean | null,
  ): boolean {
    const requiredValue = canonicalScalar(value);
    for (const encoded of facts) {
      if (!encoded.startsWith('v:')) continue;
      const separator = encoded.indexOf('=');
      const keyPath = encoded.slice(2, separator).split('.');
      const encodedValue = encoded.slice(separator + 1);
      if (
        encodedValue === requiredValue &&
        startsWith(keyPath, prefix) &&
        hasContainedSuffix(keyPath, prefix, suffix)
      ) {
        return true;
      }
    }
    return false;
  }
}

function endsWith(keyPath: readonly string[], suffix: readonly string[]): boolean {
  if (suffix.length > keyPath.length) return false;
  const offset = keyPath.length - suffix.length;
  for (let i = 0; i < suffix.length; i += 1) {
    if (keyPath[offset + i] !== suffix[i]) return false;
  }
  return true;
}

function startsWith(keyPath: readonly string[], prefix: readonly string[]): boolean {
  if (prefix.length > keyPath.length) return false;
  for (let i = 0; i < prefix.length; i += 1) {
    if (keyPath[i] !== prefix[i]) return false;
  }
  return true;
}

/**
 * `suffix` must begin immediately at or after the anchored prefix, with any
 * number of intermediate wildcard-resolved segments in between.
 */
function hasContainedSuffix(
  keyPath: readonly string[],
  prefix: readonly string[],
  suffix: readonly string[],
): boolean {
  if (suffix.length === 0) return true;
  const earliest = prefix.length;
  const latest = keyPath.length - suffix.length;
  for (let start = earliest; start <= latest; start += 1) {
    let match = true;
    for (let i = 0; i < suffix.length; i += 1) {
      if (keyPath[start + i] !== suffix[i]) {
        match = false;
        break;
      }
    }
    if (match) return true;
  }
  return false;
}

function intersect(left: Set<number>, right: Set<number>): Set<number> {
  const [small, large] = left.size <= right.size ? [left, right] : [left, right];
  const out = new Set<number>();
  for (const id of small) {
    if (large.has(id)) out.add(id);
  }
  return out;
}

export function encodeFact(fact: IndexFact): string {
  const path = fact.keyPath.join('.');
  return fact.kind === 'key' ? `k:${path}` : `v:${path}=${canonicalScalar(fact.value)}`;
}

/**
 * Collect every index fact of a document. Arrays are transparent: the same
 * key path is used for all array elements, mirroring jsonb_path_ops hashing.
 */
export function collectFacts(value: JsonValue, prefix: readonly string[] = []): IndexFact[] {
  const facts: IndexFact[] = [];
  walk(value, prefix, facts);
  return facts;
}

function walk(value: JsonValue, prefix: readonly string[], facts: IndexFact[]): void {
  if (Array.isArray(value)) {
    for (const element of value) walk(element, prefix, facts);
    return;
  }
  if (value !== null && typeof value === 'object') {
    for (const key of Object.keys(value)) {
      const childPath = [...prefix, key];
      facts.push({ kind: 'key', keyPath: childPath });
      walk(value[key], childPath, facts);
    }
    return;
  }
  if (prefix.length > 0) {
    facts.push({ kind: 'leaf', keyPath: prefix, value: value as string | number | boolean | null });
  }
}

export { FULL_SCAN, extractIndexPlan };
