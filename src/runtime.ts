import { canonicalJson, ctorId, graphCtorId, sha256Hex } from "./canonical.ts";
import { edgeLabel, normalizeGraph } from "./graph.ts";
import type { NormalizedNode } from "./graph.ts";
import type {
  AdmissionResult,
  BindingTable,
  CanonicalGrammar,
  InclusionCertificate,
  RejectionLocation,
  TypeGraph,
  VersionRange,
} from "./types.ts";

export interface CertifiedRuleBundle {
  table: BindingTable;
  grammar: CanonicalGrammar;
  certificate: InclusionCertificate;
  ranges: Map<string, VersionRange>;
}

export interface AdmissionRequest {
  tenant: string;
  graph: TypeGraph;
  /** Tables released together; the first certified bundle matching the
   *  message's assembly identities wins, so two table versions can coexist. */
  bundles: CertifiedRuleBundle[];
}

interface MatchState {
  nodeId: string;
  nonterminal: string;
  parent?: MatchState;
  edge?: string;
  refDepth: number;
}

/**
 * Runtime admission. The same rule version that was certified at release time
 * is re-checked here; no constructors are invoked, the graph is treated as
 * inert descriptor data and traversal is fully iterative.
 */
export function admit(request: AdmissionRequest): AdmissionResult {
  if (request.bundles.length === 0) {
    return reject(
      { path: [], nodeId: request.graph.root, kind: "unknown-constructor", detail: "no certified rule bundle provided" },
      "none",
      "",
      "",
    );
  }

  const normalizedResult = normalizeGraph(request.graph);
  const graphFailure = normalizedResult.failure;
  if (graphFailure) {
    const first = request.bundles[0];
    return reject(
      {
        path: [],
        nodeId: graphFailure.nodeId,
        kind: graphFailure.kind,
        detail: graphFailure.detail,
      },
      first.table.tableId,
      first.grammar.digest,
      first.table.rulesVersion,
    );
  }

  const graph = normalizedResult.normalized!;
  const attempted: Array<{ bundle: CertifiedRuleBundle; rejection: RejectionLocation }> = [];

  for (const bundle of request.bundles) {
    const precheck = checkBundleIdentity(bundle, request.tenant);
    if (precheck) {
      attempted.push({ bundle, rejection: precheck });
      continue;
    }
    const rejection = matchAgainstGrammar(
      graph.root,
      graph.nodes,
      bundle.grammar,
      bundle.ranges,
    );
    if (!rejection) {
      return {
        admitted: true,
        rulesVersion: bundle.table.rulesVersion,
        grammarDigest: bundle.grammar.digest,
        tableId: bundle.table.tableId,
      };
    }
    attempted.push({ bundle, rejection });
  }

  const first = pickFirstRejection(attempted);
  return reject(
    first.rejection,
    first.bundle.table.tableId,
    first.bundle.grammar.digest,
    first.bundle.table.rulesVersion,
  );
}

function checkBundleIdentity(
  bundle: CertifiedRuleBundle,
  tenant: string,
): RejectionLocation | undefined {
  const { certificate, grammar, table } = bundle;
  if (certificate.verdict !== "subset") {
    return {
      path: [],
      nodeId: "",
      kind: "no-matching-production",
      detail: `refusing to run with verdict ${certificate.verdict}`,
    };
  }
  if (certificate.rulesVersion !== table.rulesVersion) {
    return {
      path: [],
      nodeId: "",
      kind: "no-matching-production",
      detail: "certificate rulesVersion does not match the binding table",
    };
  }
  if (certificate.rightGrammarDigest !== grammar.digest) {
    return {
      path: [],
      nodeId: "",
      kind: "no-matching-production",
      detail: "grammar digest is not the certified one",
    };
  }
  if (grammar.tenant !== tenant) {
    return {
      path: [],
      nodeId: "",
      kind: "no-matching-production",
      detail: `bundle certifies tenant ${grammar.tenant}, request is for ${tenant}`,
    };
  }
  const { digest, ...body } = certificate;
  if (sha256Hex(body) !== digest) {
    return {
      path: [],
      nodeId: "",
      kind: "no-matching-production",
      detail: "certificate digest failed",
    };
  }
  return undefined;
}

/**
 * Coinductive membership. State is the pair (node, nonterminal): the same
 * shared node under different generic positions therefore carries different
 * obligations and is checked independently.
 */
function matchAgainstGrammar(
  root: string,
  nodes: Map<string, NormalizedNode>,
  grammar: CanonicalGrammar,
  ranges: Map<string, VersionRange>,
): RejectionLocation | undefined {
  const accepted = new Set<string>();
  const stack: MatchState[] = [
    { nodeId: root, nonterminal: grammar.start, refDepth: 0 },
  ];
  const graphIdToCatalog = buildGraphIdIndex(grammar);

  while (stack.length > 0) {
    const state = stack.pop()!;
    const stateKey = `${state.nodeId}@${state.nonterminal}`;
    if (accepted.has(stateKey)) {
      continue;
    }
    let node = nodes.get(state.nodeId)!;
    let refDepth = state.refDepth;
    while (node.refTarget) {
      node = nodes.get(node.refTarget)!;
      refDepth++;
    }

    const productions = grammar.nonterminals.get(state.nonterminal) ?? [];
    const catalogCtorId = graphIdToCatalog.get(node.ctorId!);
    const matched = productions.find(
      (production) => production.ctorId === catalogCtorId,
    );
    if (!matched) {
      const detail = describeConstructorFailure(node, grammar, ranges, graphIdToCatalog);
      return {
        path: buildPath(state, nodes),
        nodeId: node.id,
        kind: detail.kind,
        detail: detail.detail,
      };
    }
    const descriptor = grammar.catalog.get(matched.ctorId);
    if (!descriptor || (node.children ?? []).length !== descriptor.arity) {
      return {
        path: buildPath(state, nodes),
        nodeId: node.id,
        kind: "arity-mismatch",
        detail: `constructor ${node.ctorId} arity does not match the certified descriptor`,
      };
    }
    accepted.add(stateKey);
    const children = node.children!;
    for (let slot = children.length - 1; slot >= 0; slot--) {
      stack.push({
        nodeId: children[slot],
        nonterminal: matched.children[slot],
        parent: state,
        edge: edgeLabel(node, slot),
        refDepth,
      });
    }
  }
  return undefined;
}

/** Walk parent links once, only when a rejection actually has to be reported. */
function buildPath(
  state: MatchState,
  nodes: Map<string, NormalizedNode>,
): string[] {
  const edges: string[] = [];
  let current: MatchState | undefined = state;
  while (current && current.edge) {
    edges.push(current.edge);
    current = current.parent;
  }
  edges.reverse();
  let node = nodes.get(state.nodeId)!;
  while (node.refTarget) {
    edges.push(`ref->${node.refTarget}`);
    node = nodes.get(node.refTarget)!;
  }
  return edges;
}

function describeConstructorFailure(
  node: NormalizedNode,
  grammar: CanonicalGrammar,
  ranges: Map<string, VersionRange>,
  graphIdToCatalog: Map<string, string>,
): { kind: RejectionLocation["kind"]; detail: string } {
  const catalogCtorId = graphIdToCatalog.get(node.ctorId!);
  const descriptor = catalogCtorId ? grammar.catalog.get(catalogCtorId) : undefined;
  if (!descriptor) {
    return {
      kind: "unknown-constructor",
      detail: `constructor ${node.ctorId} is not part of the certified catalog`,
    };
  }
  if (!descriptor.array) {
    const range = ranges.get(ctorId(descriptor));
    const exactVersion = descriptor.assemblyVersion;
    if (range && !rangeContainsVersion(range, exactVersion)) {
      return {
        kind: "version-not-permitted",
        detail: `version ${exactVersion} is outside the certified range`,
      };
    }
  }
  return {
    kind: "no-matching-production",
    detail: `no production accepts ${node.ctorId} at this position`,
  };
}

/** Map message-side constructor identities (variance-free) to catalog ids. */
function buildGraphIdIndex(
  grammar: CanonicalGrammar,
): Map<string, string> {
  const index = new Map<string, string>();
  for (const descriptor of grammar.catalog.values()) {
    if (descriptor.array) {
      index.set(ctorId(descriptor), ctorId(descriptor));
      continue;
    }
    index.set(
      graphCtorId({
        assemblyPublicKey: descriptor.assemblyPublicKey,
        assemblyVersion: descriptor.assemblyVersion,
        typeName: descriptor.typeName,
        arity: descriptor.arity,
      }),
      ctorId(descriptor),
    );
  }
  return index;
}

function rangeContainsVersion(
  range: VersionRange,
  version: string,
): boolean {
  if (range.any) {
    return true;
  }
  if (range.exact) {
    return range.exact.includes(version);
  }
  return true;
}

function pickFirstRejection(
  attempted: Array<{ bundle: CertifiedRuleBundle; rejection: RejectionLocation }>,
): { bundle: CertifiedRuleBundle; rejection: RejectionLocation } {
  return [...attempted].sort((a, b) => {
    const lengthDiff = a.rejection.path.length - b.rejection.path.length;
    if (lengthDiff !== 0) {
      return lengthDiff;
    }
    return canonicalJson(a.rejection).localeCompare(canonicalJson(b.rejection));
  })[0];
}

function reject(
  rejection: RejectionLocation,
  tableId: string,
  grammarDigest: string,
  rulesVersion: string,
): AdmissionResult {
  return { admitted: false, rejection, rulesVersion, grammarDigest, tableId };
}
