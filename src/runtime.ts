import type {
  AdmissionResult,
  Grammar,
  GrammarAlt,
  InclusionCertificate,
  Rejection,
  TypeGraph,
  TypeNode,
  TypeRef,
} from './types.ts';
import { validateGraph } from './graph.ts';
import { assemblyMatches } from './version.ts';
import { canonicalHash, RULE_VERSION } from './inclusion.ts';

export class AdmissionError extends Error {
  readonly rejection: Rejection;
  constructor(message: string, rejection: Rejection) {
    super(message);
    this.name = 'AdmissionError';
    this.rejection = rejection;
  }
}

interface Frame {
  node: TypeNode;
  nt: string;
  parent: Frame | undefined;
  position: number;
  alt: GrammarAlt | undefined;
  nextChild: number;
  pendingChildren: number;
  failedChildren: number;
}

/**
 * Runtime admission gate.
 *
 *  - only runs with a certificate produced by this exact rule version, and
 *    re-hashes the grammar so a swapped grammar cannot reuse a certificate;
 *  - the same shared node may be accepted at one generic position and
 *    rejected at another: memoization is keyed by (node id, nonterminal);
 *  - cycles are resolved coinductively (gray = hypothesis). This is sound
 *    because every recursive reference crosses an admitted container;
 *  - all traversal is iterative and frames only store parent pointers, so
 *    inputs deeper than the JS call stack use O(depth) flat memory;
 *  - constructors are never invoked: nodes are treated as inert data.
 */
export function admit(
  graph: TypeGraph,
  grammar: Grammar,
  certificate: InclusionCertificate,
): AdmissionResult {
  const ruleVersion = grammar.ruleVersion;
  const versionError = verifyCertificateForGrammar(certificate, grammar);
  if (versionError !== undefined) {
    return { admitted: false, rejection: versionError, ruleVersion };
  }

  const containerHeads = collectContainerHeads(grammar);
  const isContainer = (candidate: TypeNode): boolean =>
    containerHeads.has(`${candidate.assembly.publicKey}|${candidate.typeName}|${candidate.arrayRank ?? 0}`);

  try {
    validateGraph(graph, isContainer);
  } catch (error) {
    return { admitted: false, rejection: toRejection(error), ruleVersion };
  }

  const byNt = new Map(grammar.productions.map((production) => [production.nt, production]));
  // true/false = settled; a present undefined entry means gray (hypothesis).
  const memo = new Map<string, boolean | undefined>();

  const rootNode = resolveNode(graph, graph.root);
  const rootFrame: Frame = {
    node: rootNode,
    nt: grammar.start,
    parent: undefined,
    position: -1,
    alt: undefined,
    nextChild: 0,
    pendingChildren: 0,
    failedChildren: 0,
  };
  const stack: Frame[] = [rootFrame];
  const keyOf = (frame: Frame): string => `${frame.node.id}\u0000${frame.nt}`;

  const rejectHere = (frame: Frame, reason: string): AdmissionResult => ({
    admitted: false,
    rejection: { path: pathOf(frame), nodeId: frame.node.id, reason },
    ruleVersion,
  });

  while (stack.length > 0) {
    const frame = stack[stack.length - 1];
    const key = keyOf(frame);

    if (memo.has(key) && memo.get(key) !== undefined) {
      settle(stack, memo, keyOf);
      continue;
    }
    if (!memo.has(key)) memo.set(key, undefined);

    const production = byNt.get(frame.nt);
    if (production === undefined) {
      return rejectHere(frame, `grammar has no production for nonterminal ${frame.nt}`);
    }

    if (frame.alt === undefined) {
      const matching = production.alts.find((candidate) => nodeMatchesAlt(frame.node, candidate));
      if (matching === undefined) {
        memo.set(key, false);
        return rejectHere(frame, `no alternative of ${frame.nt} accepts ${describeNode(frame.node)}`);
      }
      frame.alt = matching;
      frame.pendingChildren = childCount(matching);
    }

    if (frame.nextChild >= frame.pendingChildren) {
      const accepted = frame.failedChildren === 0;
      memo.set(key, accepted);
      settle(stack, memo, keyOf);
      continue;
    }

    const child = nextChildSpec(graph, frame);
    frame.nextChild += 1;
    if (child === undefined) {
      memo.set(key, false);
      return rejectHere(frame, 'missing generic argument or array element');
    }
    const childKey = `${child.node.id}\u0000${child.nt}`;
    if (memo.has(childKey)) {
      if (memo.get(childKey) === false) frame.failedChildren += 1;
      continue;
    }
    stack.push({
      node: child.node,
      nt: child.nt,
      parent: frame,
      position: child.position,
      alt: undefined,
      nextChild: 0,
      pendingChildren: 0,
      failedChildren: 0,
    });
  }

  const rootAccepted = memo.get(keyOf(rootFrame));
  if (rootAccepted !== true) {
    return {
      admitted: false,
      rejection: { path: [], nodeId: rootNode.id, reason: 'root node rejected by grammar' },
      ruleVersion,
    };
  }
  return { admitted: true, ruleVersion };
}

function settle(
  frames: Frame[],
  memo: Map<string, boolean | undefined>,
  keyOf: (frame: Frame) => string,
): void {
  const frame = frames.pop()!;
  const result = memo.get(keyOf(frame));
  if (frame.parent !== undefined && result === false) {
    frame.parent.failedChildren += 1;
  }
}

function childCount(alt: GrammarAlt): number {
  if (alt.arrayRank !== undefined && alt.arrayRank > 0) return alt.element === undefined ? 0 : 1;
  return alt.args?.length ?? 0;
}

function nextChildSpec(
  graph: TypeGraph,
  frame: { node: TypeNode; alt: GrammarAlt; nextChild: number },
): { node: TypeNode; nt: string; position: number } | undefined {
  const alt = frame.alt;
  const index = frame.nextChild;
  if (alt.arrayRank !== undefined && alt.arrayRank > 0) {
    if (frame.node.element === undefined || alt.element === undefined) return undefined;
    return { node: resolveNode(graph, frame.node.element), nt: alt.element, position: 0 };
  }
  const spec = alt.args?.[index];
  const ref = frame.node.args?.[index];
  if (spec === undefined || ref === undefined) return undefined;
  return { node: resolveNode(graph, ref), nt: spec.nt, position: index };
}

function pathOf(frame: { parent: unknown; position: number }): number[] {
  const path: number[] = [];
  let current: { parent: unknown; position: number } | undefined = frame;
  while (current !== undefined && current.position >= 0) {
    path.push(current.position);
    current = current.parent as { parent: unknown; position: number } | undefined;
  }
  path.reverse();
  return path;
}

function verifyCertificateForGrammar(
  certificate: InclusionCertificate,
  grammar: Grammar,
): Rejection | undefined {
  if (certificate.ruleVersion !== RULE_VERSION || grammar.ruleVersion !== RULE_VERSION) {
    return {
      path: [],
      nodeId: '',
      reason: `rule version mismatch: certificate ${certificate.ruleVersion}, grammar ${grammar.ruleVersion}, runtime ${RULE_VERSION}`,
    };
  }
  if (certificate.verdict !== 'subset') {
    return {
      path: [],
      nodeId: '',
      reason: `certificate verdict is ${certificate.verdict}; only subset certificates may gate admission`,
    };
  }
  if (certificate.newGrammarHash !== canonicalHash(grammar)) {
    return {
      path: [],
      nodeId: '',
      reason: 'grammar hash does not match the certified grammar',
    };
  }
  return undefined;
}

function resolveNode(graph: TypeGraph, ref: TypeRef): TypeNode {
  if (typeof ref !== 'string') return ref;
  const node = graph.nodes[ref];
  if (node === undefined) {
    throw new AdmissionError('dangling node reference', {
      path: [],
      nodeId: ref,
      reason: `node ${ref} not found`,
    });
  }
  return node;
}

function nodeMatchesAlt(node: TypeNode, alt: GrammarAlt): boolean {
  if (node.typeName !== alt.typeName) return false;
  if ((node.arrayRank ?? 0) !== (alt.arrayRank ?? 0)) return false;
  if ((node.args ?? []).length !== (alt.args ?? []).length) return false;
  return assemblyMatches(alt.assembly, node.assembly);
}

function describeNode(node: TypeNode): string {
  const rank = node.arrayRank !== undefined && node.arrayRank > 0 ? `[rank:${node.arrayRank}]` : '';
  return `${node.assembly.publicKey}/${node.typeName}@${node.assembly.version}${rank}`;
}

function collectContainerHeads(grammar: Grammar): Set<string> {
  const heads = new Set<string>();
  for (const production of grammar.productions) {
    for (const alt of production.alts) {
      if ((alt.args?.length ?? 0) > 0 || alt.element !== undefined) {
        heads.add(`${alt.assembly.publicKey}|${alt.typeName}|${alt.arrayRank ?? 0}`);
      }
    }
  }
  return heads;
}

function toRejection(error: unknown): Rejection {
  if (error && typeof error === 'object' && 'nodeId' in error) {
    const candidate = error as { nodeId: unknown; path?: number[]; message?: string };
    return {
      path: candidate.path ?? [],
      nodeId: String(candidate.nodeId),
      reason: candidate.message ?? 'graph validation failed',
    };
  }
  return { path: [], nodeId: '', reason: String(error) };
}
