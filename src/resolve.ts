import { acceptsPath } from "./dfa.ts";
import { canonicalize } from "./canonical.ts";
import {
  findMembershipProof,
  resourceIdentityKey,
  verifyAssetRoot,
  verifyMembershipProof,
} from "./membership.ts";
import {
  publicKeyFingerprint,
  verifyPolicySource,
  verifyTenantRoot,
  verifyWaiver,
} from "./signature.ts";
import { intervalContains, toEpochMs } from "./time.ts";
import type {
  ConstraintDeclaration,
  ConstraintOrigin,
  ConstraintValue,
  EffectiveConstraint,
  EffectivePolicy,
  InheritanceEdge,
  InheritanceGraph,
  MembershipProof,
  PolicyNode,
  ProvenanceChain,
  ResourceIdentity,
  ResolutionRequest,
  ResourceScope,
  SignedPolicySource,
  SignedWaiver,
  TrustOptions,
  UnknownReason,
  WaiverApplication,
  WaiverTarget,
} from "./types.ts";

interface GraphView {
  nodes: Map<string, PolicyNode>;
  parents: Map<string, string[]>;
  children: Map<string, string[]>;
  edges: InheritanceEdge[];
}

interface ScopeDecision {
  applies: boolean;
  unknown?: UnknownReason;
}

interface LocalContribution {
  originKey: string;
  declaration: ConstraintDeclaration;
  nodeId: string;
}

interface OriginSupport {
  originKey: string;
  declaration: ConstraintDeclaration;
  nodeIds: Set<string>;
}

interface MergedKey {
  allowed: ConstraintValue[];
  conflict: boolean;
  origins: Map<string, OriginSupport>;
}

interface ResolutionContext {
  at: number;
  identity: ResourceIdentity;
  proofs: readonly MembershipProof[];
  trust?: TrustOptions;
  maxProvenanceChains: number;
  unknown: Map<string, UnknownReason>;
  diagnostics: string[];
}

export function buildGraphView(graph: InheritanceGraph): GraphView {
  const nodes = new Map<string, PolicyNode>();
  for (const node of graph.nodes) {
    if (nodes.has(node.id)) {
      throw new Error(`Duplicate policy node id: ${node.id}`);
    }
    nodes.set(node.id, node);
  }
  const parents = new Map<string, string[]>();
  const children = new Map<string, string[]>();
  for (const node of graph.nodes) {
    parents.set(node.id, []);
    children.set(node.id, []);
  }
  const edgeKeys = new Set<string>();
  const edges: InheritanceEdge[] = [];
  const addEdge = (from: string, to: string) => {
    if (!nodes.has(from)) {
      throw new Error(`Inheritance edge references unknown parent: ${from}`);
    }
    if (!nodes.has(to)) {
      throw new Error(`Inheritance edge references unknown child: ${to}`);
    }
    const key = `${from}\u0000${to}`;
    if (edgeKeys.has(key)) {
      return;
    }
    edgeKeys.add(key);
    edges.push({ from, to });
    parents.get(to)?.push(from);
    children.get(from)?.push(to);
  };
  for (const node of graph.nodes) {
    for (const parent of node.parents ?? []) {
      addEdge(parent, node.id);
    }
  }
  for (const edge of graph.edges ?? []) {
    addEdge(edge.from, edge.to);
  }
  for (const list of parents.values()) {
    list.sort();
  }
  for (const list of children.values()) {
    list.sort();
  }
  return { nodes, parents, children, edges };
}

function unknownKey(reason: UnknownReason): string {
  return [
    reason.code,
    reason.sourceId,
    reason.waiverId,
    reason.nodeId,
    reason.setId,
  ].join("\u0000");
}

function addUnknown(context: ResolutionContext, reason: UnknownReason): void {
  context.unknown.set(unknownKey(reason), reason);
}

function evaluateScope(
  scope: ResourceScope | undefined,
  context: ResolutionContext,
  labels: { sourceId?: string; waiverId?: string; nodeId?: string; setId?: string },
): ScopeDecision {
  if (!scope) {
    return { applies: true };
  }
  if (!acceptsPath(scope.path, context.identity.path)) {
    return { applies: false };
  }
  if (!scope.assets) {
    return { applies: true };
  }
  if (
    scope.assets.tenantId !== context.identity.tenantRoot.tenantId ||
    scope.assets.tenantRootId !== context.identity.tenantRoot.rootId
  ) {
    return { applies: false };
  }
  const rootCheck = verifyAssetRoot(scope.assets, context.identity.tenantRoot, {
    at: context.at,
    trust: context.trust,
  });
  if (!rootCheck.ok) {
    return {
      applies: true,
      unknown: {
        code: rootCheck.reason === "expired-asset-root" ? "expired-asset-root" : "invalid-asset-root",
        message: `Asset set ${scope.assets.setId} failed verification: ${rootCheck.reason ?? "unknown"}`,
        ...labels,
        setId: scope.assets.setId,
      },
    };
  }
  const proof = findMembershipProof(
    context.identity,
    [...(scope.proofs ?? []), ...context.proofs],
    scope.assets.setId,
  );
  if (!proof) {
    return {
      applies: true,
      unknown: {
        code: "missing-membership-proof",
        message: `No membership proof for ${context.identity.resourceId} in asset set ${scope.assets.setId}`,
        ...labels,
        setId: scope.assets.setId,
      },
    };
  }
  if (
    !verifyMembershipProof(proof, context.identity, scope.assets, context.identity.tenantRoot, {
      at: context.at,
      trust: context.trust,
    })
  ) {
    return {
      applies: true,
      unknown: {
        code: "invalid-membership-proof",
        message: `Membership proof for ${context.identity.resourceId} is not valid in asset set ${scope.assets.setId}`,
        ...labels,
        setId: scope.assets.setId,
      },
    };
  }
  return { applies: true };
}

function sourceVerification(
  source: SignedPolicySource,
  context: ResolutionContext,
): UnknownReason | undefined {
  const check = verifyPolicySource(source, {
    at: context.at,
    trust: context.trust,
  });
  if (check.ok) {
    return undefined;
  }
  return {
    code: check.reason ?? "invalid-source-signature",
    message: `Policy source ${source.id} failed verification: ${check.reason ?? "unknown"}`,
    sourceId: source.id,
  };
}

function originKey(declaration: ConstraintDeclaration): string {
  return `${declaration.source.id}\u0000${publicKeyFingerprint(declaration.source.publicKey)}\u0000${declaration.id}`;
}

function declarationFingerprint(declaration: ConstraintDeclaration): string {
  return canonicalize({
    key: declaration.key,
    allowed: [...declaration.allowed].sort(),
    literal: declaration.literal,
    valid: declaration.valid,
    scope: declaration.scope,
  });
}

function waiverTargetSourceId(target: WaiverTarget): string | undefined {
  return target.sourceId ?? target.originId;
}

function waiverTargetConstraintId(target: WaiverTarget): string | undefined {
  return target.constraintId ?? target.declarationId;
}

export function resolveEffectivePolicy(request: ResolutionRequest): EffectivePolicy {
  const at = toEpochMs(request.state.at);
  const context: ResolutionContext = {
    at,
    identity: request.identity,
    proofs: request.membershipProofs ?? [],
    trust: request.trust,
    maxProvenanceChains: request.maxProvenanceChains ?? 128,
    unknown: new Map(),
    diagnostics: [],
  };
  const identityKey = resourceIdentityKey(request.identity);
  const rootCheck = verifyTenantRoot(request.identity.tenantRoot, {
    at,
    trust: request.trust,
  });
  if (!rootCheck.ok) {
    addUnknown(context, {
      code: rootCheck.reason ?? "invalid-tenant-root",
      message: `Tenant root for ${request.identity.tenantRoot.tenantId} failed verification`,
    });
    return {
      status: "unknown",
      target: request.target,
      identityKey,
      constraints: {},
      unknown: [...context.unknown.values()],
      waived: [],
      diagnostics: context.diagnostics,
    };
  }

  const graph = buildGraphView(request.state.graph);
  if (!graph.nodes.has(request.target)) {
    throw new Error(`Unknown target node: ${request.target}`);
  }
  const relevantNodes = ancestorsOf(graph, request.target);
  const local = new Map<string, Map<string, LocalContribution[]>>();
  const allDeclarations = new Map<string, LocalContribution[]>();

  for (const node of graph.nodes.values()) {
    const byKey = new Map<string, LocalContribution[]>();
    local.set(node.id, byKey);
    if (!relevantNodes.has(node.id)) {
      continue;
    }
    for (const declaration of node.declarations ?? []) {
      const sourceError = sourceVerification(declaration.source, context);
      if (sourceError) {
        addUnknown(context, { ...sourceError, nodeId: node.id });
        continue;
      }
      if (!intervalContains(declaration.valid, at)) {
        continue;
      }
      const nodeScope = evaluateScope(node.scope, context, {
        sourceId: declaration.source.id,
        nodeId: node.id,
      });
      if (nodeScope.unknown) {
        addUnknown(context, nodeScope.unknown);
      }
      if (!nodeScope.applies || nodeScope.unknown) {
        continue;
      }
      const declarationScope = evaluateScope(declaration.scope, context, {
        sourceId: declaration.source.id,
        nodeId: node.id,
      });
      if (declarationScope.unknown) {
        addUnknown(context, declarationScope.unknown);
      }
      if (!declarationScope.applies || declarationScope.unknown) {
        continue;
      }
      const contribution: LocalContribution = {
        originKey: originKey(declaration),
        declaration,
        nodeId: node.id,
      };
      const existingAtOrigin = allDeclarations.get(contribution.originKey);
      if (
        existingAtOrigin !== undefined &&
        existingAtOrigin.some(
          (existing) =>
            declarationFingerprint(existing.declaration) !== declarationFingerprint(declaration),
        )
      ) {
        addUnknown(context, {
          code: "conflicting-constraint-origin",
          message: `Constraint origin ${declaration.source.id}/${declaration.id} has inconsistent signed content`,
          sourceId: declaration.source.id,
          nodeId: node.id,
        });
        continue;
      }
      const list = byKey.get(declaration.key) ?? [];
      list.push(contribution);
      byKey.set(declaration.key, list);
      const globalList = allDeclarations.get(contribution.originKey) ?? [];
      globalList.push(contribution);
      allDeclarations.set(contribution.originKey, globalList);
    }
  }

  const waivers = request.state.waivers ?? [];
  const cancellations = new Map<string, WaiverApplication[]>();
  const keyByOrigin = new Map<string, string>();
  for (const [origin, supports] of allDeclarations) {
    const key = supports[0]?.declaration.key;
    if (key !== undefined) {
      keyByOrigin.set(origin, key);
    }
  }
  for (const waiver of waivers) {
    verifyAndApplyWaiver(waiver, allDeclarations, context, cancellations);
  }

  for (const byKey of local.values()) {
    for (const [key, contributions] of byKey) {
      byKey.set(
        key,
        contributions.filter((contribution) => !cancellations.has(contribution.originKey)),
      );
    }
  }

  const states = computeFixedPoint(graph, local);
  const targetState = states.get(request.target) ?? new Map<string, MergedKey>();
  const constraints: Record<string, EffectiveConstraint> = {};
  for (const [key, merged] of [...targetState.entries()].sort(([a], [b]) => a.localeCompare(b))) {
    const origins: ConstraintOrigin[] = [];
    for (const support of [...merged.origins.values()].sort((a, b) =>
      a.originKey.localeCompare(b.originKey),
    )) {
      const chains: ProvenanceChain[] = [];
      for (const declarationNode of [...support.nodeIds].sort()) {
        const paths = enumerateSimplePaths(
          graph.children,
          declarationNode,
          request.target,
          context.maxProvenanceChains,
        );
        if (paths.length === 0) {
          context.diagnostics.push(
            `Origin ${support.originKey} at ${declarationNode} has no simple chain to ${request.target}`,
          );
        }
        for (const nodeIds of paths) {
          chains.push({
            sourceId: support.declaration.source.id,
            sourceKeyFingerprint: publicKeyFingerprint(support.declaration.source.publicKey),
            declarationId: support.declaration.id,
            nodeIds,
            edges: nodeIds.slice(0, -1).map((from, index) => ({
              from,
              to: nodeIds[index + 1],
            })),
          });
        }
      }
      origins.push({
        sourceId: support.declaration.source.id,
        sourceKeyFingerprint: publicKeyFingerprint(support.declaration.source.publicKey),
        declarationId: support.declaration.id,
        nodeId: [...support.nodeIds].sort()[0] ?? request.target,
        literal: support.declaration.literal,
        allowed: [...support.declaration.allowed],
        chains,
      });
    }
    const waived = [...cancellations.entries()]
      .filter(([origin]) => keyByOrigin.get(origin) === key)
      .flatMap(([, applications]) => applications)
      .sort((a, b) => a.waiverId.localeCompare(b.waiverId));
    constraints[key] = {
      key,
      status: merged.conflict ? "conflict" : "active",
      allowed: merged.allowed,
      origins,
      waived,
    };
  }

  const unknown = [...context.unknown.values()];
  const status = unknown.length > 0
    ? "unknown"
    : Object.values(constraints).some((constraint) => constraint.status === "conflict")
      ? "conflict"
      : "resolved";
  const waived = [...new Set([...cancellations.values()].flat())].sort((a, b) => {
    const byWaiver = a.waiverId.localeCompare(b.waiverId);
    return byWaiver !== 0 ? byWaiver : a.declarationId.localeCompare(b.declarationId);
  });
  return {
    status,
    target: request.target,
    identityKey,
    constraints,
    unknown,
    waived,
    diagnostics: context.diagnostics,
  };
}

export const resolvePolicy = resolveEffectivePolicy;
export const computeEffectivePolicy = resolveEffectivePolicy;
export const resolveEffectiveConstraints = resolveEffectivePolicy;

function verifyAndApplyWaiver(
  waiver: SignedWaiver,
  declarations: Map<string, LocalContribution[]>,
  context: ResolutionContext,
  cancellations: Map<string, WaiverApplication[]>,
): void {
  if (!intervalContains(
    {
      start: waiver.issuedAt,
      end: waiver.expiresAt,
      startInclusive: true,
      endInclusive: false,
    },
    context.at,
  )) {
    return;
  }
  const sourceId = waiverTargetSourceId(waiver.target);
  if (!sourceId) {
    context.diagnostics.push(`Waiver ${waiver.id} has no target source`);
    return;
  }
  const check = verifyWaiver(waiver, {
    at: context.at,
    trust: context.trust,
  });
  if (!check.ok) {
    if (check.reason !== "expired") {
      const code = check.reason === "invalid-waiver-signature"
        ? "invalid-waiver-signature"
        : check.reason === "invalid-source-signature"
          ? "invalid-source-signature"
          : check.reason === "expired-source" || check.reason === "untrusted-source"
            ? "untrusted-source"
            : "invalid-waiver-signature";
      addUnknown(context, {
        code,
        message: `Waiver ${waiver.id} failed verification: ${check.reason ?? "unknown"}`,
        sourceId,
        waiverId: waiver.id,
      });
    }
    return;
  }
  const constraintId = waiverTargetConstraintId(waiver.target);
  const candidates = [...declarations.entries()]
    .filter(([, supports]) =>
      supports.some(
        ({ declaration }) =>
          declaration.source.id === sourceId &&
          (constraintId === undefined || declaration.id === constraintId) &&
          (waiver.target.key === undefined || declaration.key === waiver.target.key) &&
          (constraintId !== undefined || waiver.target.key !== undefined),
      ),
    );
  const uniqueOrigins = [...new Set(candidates.map(([key]) => key))];
  if (uniqueOrigins.length === 0) {
    context.diagnostics.push(
      `Waiver ${waiver.id} targets a source/constraint that is not active for this identity`,
    );
    return;
  }
  if (uniqueOrigins.length > 1) {
    addUnknown(context, {
      code: "ambiguous-waiver-target",
      message: `Waiver ${waiver.id} matches more than one constraint from source ${sourceId}`,
      sourceId,
      waiverId: waiver.id,
    });
    return;
  }

  const scope = evaluateScope(waiver.scope, context, {
    sourceId,
    waiverId: waiver.id,
  });
  if (scope.unknown) {
    addUnknown(context, scope.unknown);
    return;
  }
  if (!scope.applies) {
    return;
  }

  const targetOrigin = uniqueOrigins[0];
  const support = candidates.find(([key]) => key === targetOrigin)?.[1][0];
  if (!support) {
    return;
  }
  const application: WaiverApplication = {
    waiverId: waiver.id,
    sourceId,
    declarationId: support.declaration.id,
    issuerId: waiver.issuer.id,
  };
  const existing = cancellations.get(targetOrigin) ?? [];
  existing.push(application);
  cancellations.set(targetOrigin, existing);
}

function mergeContributions(contributions: readonly LocalContribution[]): MergedKey | undefined {
  if (contributions.length === 0) {
    return undefined;
  }
  const sorted = [...contributions].sort((a, b) => {
    const byOrigin = a.originKey.localeCompare(b.originKey);
    return byOrigin !== 0 ? byOrigin : a.nodeId.localeCompare(b.nodeId);
  });
  let allowed = [...new Set(sorted[0]?.declaration.allowed ?? [])].sort();
  let conflict = allowed.length === 0;
  const origins = new Map<string, OriginSupport>();
  for (const contribution of sorted) {
    const candidate = [...new Set(contribution.declaration.allowed)];
    if (candidate.length === 0) {
      conflict = true;
    }
    if (!conflict) {
      const candidateSet = new Set(candidate);
      allowed = allowed.filter((value) => candidateSet.has(value));
      if (allowed.length === 0) {
        conflict = true;
      }
    }
    const existing = origins.get(contribution.originKey);
    if (existing) {
      existing.nodeIds.add(contribution.nodeId);
    } else {
      origins.set(contribution.originKey, {
        originKey: contribution.originKey,
        declaration: contribution.declaration,
        nodeIds: new Set([contribution.nodeId]),
      });
    }
  }
  return {
    allowed: conflict ? [] : allowed,
    conflict,
    origins,
  };
}

function mergeKeyStates(
  contributions: readonly LocalContribution[],
  incoming: readonly MergedKey[],
): MergedKey | undefined {
  if (contributions.length === 0 && incoming.length === 0) {
    return undefined;
  }
  const all: LocalContribution[] = [...contributions];
  for (const state of incoming) {
    for (const support of state.origins.values()) {
      for (const nodeId of support.nodeIds) {
        all.push({
          originKey: support.originKey,
          declaration: support.declaration,
          nodeId,
        });
      }
    }
  }
  return mergeContributions(all);
}

function sameMergedKey(left: MergedKey | undefined, right: MergedKey | undefined): boolean {
  if (!left || !right) {
    return left === right;
  }
  if (left.conflict !== right.conflict) {
    return false;
  }
  if (left.allowed.length !== right.allowed.length) {
    return false;
  }
  if (left.allowed.some((value, index) => value !== right.allowed[index])) {
    return false;
  }
  if (left.origins.size !== right.origins.size) {
    return false;
  }
  for (const [key, support] of left.origins) {
    const other = right.origins.get(key);
    if (!other) {
      return false;
    }
    if (support.nodeIds.size !== other.nodeIds.size) {
      return false;
    }
    const a = [...support.nodeIds].sort();
    const b = [...other.nodeIds].sort();
    if (a.some((nodeId, index) => nodeId !== b[index])) {
      return false;
    }
  }
  return true;
}

function sameState(
  left: Map<string, MergedKey>,
  right: Map<string, MergedKey>,
): boolean {
  if (left.size !== right.size) {
    return false;
  }
  for (const [key, value] of left) {
    if (!sameMergedKey(value, right.get(key))) {
      return false;
    }
  }
  return true;
}

function computeFixedPoint(
  graph: GraphView,
  local: Map<string, Map<string, LocalContribution[]>>,
): Map<string, Map<string, MergedKey>> {
  const states = new Map<string, Map<string, MergedKey>>();
  const queue: string[] = [...graph.nodes.keys()];
  const queued = new Set(queue);
  for (const nodeId of queue) {
    states.set(nodeId, new Map());
  }

  while (queue.length > 0) {
    const nodeId = queue.shift() as string;
    queued.delete(nodeId);
    const incomingKeys = new Set<string>();
    for (const parent of graph.parents.get(nodeId) ?? []) {
      for (const key of states.get(parent)?.keys() ?? []) {
        incomingKeys.add(key);
      }
    }
    const nodeLocal = local.get(nodeId) ?? new Map<string, LocalContribution[]>();
    const allKeys = new Set([...nodeLocal.keys(), ...incomingKeys]);
    const next = new Map<string, MergedKey>();
    for (const key of [...allKeys].sort()) {
      const incoming: MergedKey[] = [];
      for (const parent of graph.parents.get(nodeId) ?? []) {
        const parentKey = states.get(parent)?.get(key);
        if (parentKey) {
          incoming.push(parentKey);
        }
      }
      const merged = mergeKeyStates(nodeLocal.get(key) ?? [], incoming);
      if (merged) {
        next.set(key, merged);
      }
    }
    if (!sameState(states.get(nodeId) ?? new Map(), next)) {
      states.set(nodeId, next);
      for (const child of graph.children.get(nodeId) ?? []) {
        if (!queued.has(child)) {
          queue.push(child);
          queued.add(child);
        }
      }
    }
  }
  return states;
}

function enumerateSimplePaths(
  children: Map<string, string[]>,
  from: string,
  to: string,
  limit: number,
): string[][] {
  if (from === to) {
    return [[from]];
  }
  const paths: string[][] = [];
  const path: string[] = [];
  const visited = new Set<string>();
  const visit = (node: string): void => {
    if (paths.length >= limit) {
      return;
    }
    path.push(node);
    visited.add(node);
    for (const child of children.get(node) ?? []) {
      if (paths.length >= limit) {
        break;
      }
      if (child === to) {
        paths.push([...path, child]);
      } else if (!visited.has(child)) {
        visit(child);
      }
    }
    visited.delete(node);
    path.pop();
  };
  visit(from);
  return paths;
}

export function validateProvenanceChain(
  graph: InheritanceGraph,
  chain: ProvenanceChain,
  expectedTarget?: string,
  options: { at?: number | string | Date; trust?: TrustOptions } = {},
): boolean {
  const view = buildGraphView(graph);
  if (chain.nodeIds.length === 0) {
    return false;
  }
  if (expectedTarget !== undefined && chain.nodeIds[chain.nodeIds.length - 1] !== expectedTarget) {
    return false;
  }
  const originNode = view.nodes.get(chain.nodeIds[0]);
  if (
    !originNode ||
    !(originNode.declarations ?? []).some(
      (declaration) =>
        declaration.source.id === chain.sourceId &&
        publicKeyFingerprint(declaration.source.publicKey) === chain.sourceKeyFingerprint &&
        verifyPolicySource(declaration.source, options).ok &&
        declaration.id === chain.declarationId,
    )
  ) {
    return false;
  }
  if (chain.edges.length !== chain.nodeIds.length - 1) {
    return false;
  }
  for (let index = 0; index < chain.edges.length; index += 1) {
    const edge = chain.edges[index];
    if (edge.from !== chain.nodeIds[index] || edge.to !== chain.nodeIds[index + 1]) {
      return false;
    }
    if (!view.edges.some((candidate) => candidate.from === edge.from && candidate.to === edge.to)) {
      return false;
    }
  }
  return true;
}

export const verifyProvenanceChain = validateProvenanceChain;

function ancestorsOf(graph: GraphView, target: string): Set<string> {
  const result = new Set<string>();
  const stack = [target];
  while (stack.length > 0) {
    const node = stack.pop() as string;
    if (result.has(node)) {
      continue;
    }
    result.add(node);
    for (const parent of graph.parents.get(node) ?? []) {
      if (!result.has(parent)) {
        stack.push(parent);
      }
    }
  }
  return result;
}
