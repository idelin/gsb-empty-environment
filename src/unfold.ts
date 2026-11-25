import { ctorId, graphCtorId } from "./canonical.ts";
import { normalizeGraph } from "./graph.ts";
import type { NormalizedGraph, NormalizedNode } from "./graph.ts";
import type {
  CanonicalGrammar,
  CanonicalProduction,
  TypeGraph,
} from "./types.ts";

export interface GroundTerm {
  ctorId: string;
  args: GroundTerm[];
}

interface MemoEntry {
  done: boolean;
  terms: GroundTerm[];
}

/**
 * Enumerate every finite unfolding of a message type graph up to `maxDepth`
 * constructor layers. Recursion is cut at the depth limit; the evaluator is
 * fully iterative with resumable frames, so guarded cycles (e.g. List) do not
 * overflow the call stack and each finite term is produced exactly once.
 */
export function unfoldGraph(
  graph: TypeGraph,
  maxDepth: number,
  grammar?: CanonicalGrammar,
): { terms: GroundTerm[]; rejectedBareAlias?: string } {
  const result = normalizeGraph(graph);
  if (result.failure) {
    return { terms: [], rejectedBareAlias: result.failure.detail };
  }
  const normalized = result.normalized!;
  const memo = new Map<string, MemoEntry>();
  interface Frame {
    key: string;
    node: NormalizedNode;
    budget: number;
    childKeys: string[];
    index: number;
    childTerms: GroundTerm[][];
  }
  const rootKey = `${normalized.root}:${maxDepth}`;
  const stack: Frame[] = [];

  const start = (key: string, nodeId: string, budget: number): void => {
    let node: NormalizedNode | undefined = normalized.nodes.get(nodeId);
    while (node && node.refTarget) {
      node = normalized.nodes.get(node.refTarget);
    }
    if (!node || !node.ctorId || !node.children || budget < 0) {
      memo.set(key, { done: true, terms: [] });
      return;
    }
    memo.set(key, { done: false, terms: [] });
    stack.push({
      key,
      node,
      budget,
      childKeys: node.children.map((child) => `${child}:${budget - 1}`),
      index: 0,
      childTerms: [],
    });
  };

  start(rootKey, normalized.root, maxDepth);
  while (stack.length > 0) {
    const frame = stack[stack.length - 1];
    if (frame.index >= frame.childKeys.length) {
      const terms = crossProduct(frame.childTerms).map((args) => ({
        ctorId: resolveCtorId(frame.node.ctorId!, grammar),
        args,
      }));
      memo.set(frame.key, { done: true, terms: dedupe(terms) });
      stack.pop();
      continue;
    }
    const childKey = frame.childKeys[frame.index];
    const childEntry = memo.get(childKey);
    if (!childEntry) {
      const [nodeId, budgetText] = childKey.split(":");
      start(childKey, nodeId, Number(budgetText));
      continue;
    }
    if (!childEntry.done) {
      frame.childTerms.push([]);
    } else {
      frame.childTerms.push(childEntry.terms);
    }
    frame.index++;
  }
  return { terms: memo.get(rootKey)?.terms ?? [] };
}

/** Enumerate every finite term of a canonical grammar up to `maxDepth`. */
export function enumerateGrammarTerms(
  grammar: CanonicalGrammar,
  maxDepth: number,
): GroundTerm[] {
  const memo = new Map<string, MemoEntry>();
  interface Frame {
    key: string;
    nonterminal: string;
    budget: number;
    activeProductions: CanonicalProduction[];
    childKeys: string[];
    index: number;
    childTerms: GroundTerm[][];
  }
  const rootKey = `${grammar.start}:${maxDepth}`;
  const stack: Frame[] = [];

  const start = (key: string, nonterminal: string, budget: number): void => {
    if (budget < 0) {
      memo.set(key, { done: true, terms: [] });
      return;
    }
    const productions = (grammar.nonterminals.get(nonterminal) ?? []).filter(
      (production) => budget > 0 || production.children.length === 0,
    );
    const childKeys: string[] = [];
    for (const production of productions) {
      for (const child of production.children) {
        childKeys.push(`${child}:${budget - 1}`);
      }
    }
    memo.set(key, { done: false, terms: [] });
    stack.push({
      key,
      nonterminal,
      budget,
      activeProductions: productions,
      childKeys,
      index: 0,
      childTerms: [],
    });
  };

  start(rootKey, grammar.start, maxDepth);
  while (stack.length > 0) {
    const frame = stack[stack.length - 1];
    if (frame.index >= frame.childKeys.length) {
      const terms: GroundTerm[] = [];
      let cursor = 0;
      for (const production of frame.activeProductions) {
        const arity = production.children.length;
        const options = frame.childTerms.slice(cursor, cursor + arity);
        cursor += arity;
        for (const args of crossProduct(options)) {
          terms.push({ ctorId: production.ctorId, args });
        }
      }
      memo.set(frame.key, { done: true, terms: dedupe(terms) });
      stack.pop();
      continue;
    }
    const childKey = frame.childKeys[frame.index];
    const childEntry = memo.get(childKey);
    if (!childEntry) {
      const [nonterminal, budgetText] = childKey.split(":");
      start(childKey, nonterminal, Number(budgetText));
      continue;
    }
    frame.childTerms.push(childEntry.done ? childEntry.terms : []);
    frame.index++;
  }
  return memo.get(rootKey)?.terms ?? [];
}

function resolveCtorId(
  graphId: string,
  grammar: CanonicalGrammar | undefined,
): string {
  if (!grammar || graphId.startsWith("array:")) {
    return graphId;
  }
  for (const descriptor of grammar.catalog.values()) {
    if (
      graphCtorId({
        assemblyPublicKey: descriptor.assemblyPublicKey,
        assemblyVersion: descriptor.assemblyVersion,
        typeName: descriptor.typeName,
        arity: descriptor.arity,
      }) === graphId
    ) {
      return ctorId(descriptor);
    }
  }
  return graphId;
}

function crossProduct(parts: GroundTerm[][]): GroundTerm[][] {
  let accumulator: GroundTerm[][] = [[]];
  for (const part of parts) {
    const next: GroundTerm[][] = [];
    for (const prefix of accumulator) {
      for (const term of part) {
        next.push([...prefix, term]);
      }
    }
    accumulator = next;
  }
  return accumulator;
}

function dedupe(terms: GroundTerm[]): GroundTerm[] {
  const seen = new Set<string>();
  const unique: GroundTerm[] = [];
  for (const term of terms) {
    const text = JSON.stringify(term);
    if (!seen.has(text)) {
      seen.add(text);
      unique.push(term);
    }
  }
  return unique;
}

export function termKey(term: GroundTerm): string {
  return JSON.stringify(term);
}
