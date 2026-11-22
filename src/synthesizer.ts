import { FixedOrderBDD, type BDDNode, type GateExpr } from './bdd.ts';
import {
  NonceAuthority,
  sha256,
  sourceMatches,
  type CspPolicy,
  type DenialCode,
  type FragmentNode,
  type LoadSpec,
  type SourceExpr,
} from './model.ts';
import type { SealedRenderConfig } from './cache.ts';

export interface PositionedScript {
  key: string;
  scriptId: string;
  fragmentId: string;
  positionPath: string[];
  commitment: string;
  source: SourceExpr;
  inheritableSources: SourceExpr[];
  /** Sources inherited from the parent slot that fills this occurrence. */
  parentInheritedSources: SourceExpr[];
  nonce?: string;
}

export interface LoadEdge {
  id: string;
  fromKey: string;
  toKey: string;
  spec: LoadSpec;
  gate: BDDNode;
}

export interface PositionedGraph {
  scripts: Map<string, PositionedScript>;
  edges: LoadEdge[];
  outgoing: Map<string, LoadEdge[]>;
}

export interface AuthorizationGrant {
  responseId: string;
  positionKey: string;
  scriptId: string;
  commitment: string;
  inheritableSources: SourceExpr[];
  nonce?: string;
}

export interface DenialReason {
  policyId: string;
  code: DenialCode;
  detail: string;
}

export interface AuthorizationHop {
  kind: 'root' | 'load';
  scriptId: string;
  positionKey: string;
  viaSlot?: string;
  edgeGate?: GateExpr;
  source?: SourceExpr;
}

export interface TargetDecision {
  targetKey: string;
  /** Exact symbolic condition under which every policy allows the target. */
  allowedCondition: BDDNode;
  perPolicy: Record<string, BDDNode>;
  allowed: boolean;
  shortestChain: AuthorizationHop[] | undefined;
  denials: DenialReason[];
}

interface PolicyEvaluation {
  /** Per-root-provenance trust tags, keyed by `${rootId}::${scriptKey}`. */
  tags: Map<string, TrustTag>;
  denials: DenialReason[];
}

interface TrustTag {
  rootId: string;
  scriptKey: string;
  condition: BDDNode;
  rootScriptId: string;
  isRoot: boolean;
}

export interface SynthesisOptions {
  publishNonces?: boolean;
  /** Nonce values that must remain unpublished even when publishing is on. */
  withholdNonces?: Iterable<string>;
}

interface Frame {
  fragment: FragmentNode;
  path: string[];
  inherited: SourceExpr[] | undefined;
}

export class FragmentSynthesizer {
  readonly bdd: FixedOrderBDD;
  readonly authority: NonceAuthority;
  readonly policies: CspPolicy[];

  private graph: PositionedGraph | undefined;
  private grants: AuthorizationGrant[] = [];

  constructor(input: { bdd: FixedOrderBDD; authority: NonceAuthority; policies: CspPolicy[] }) {
    this.bdd = input.bdd;
    this.authority = input.authority;
    this.policies = input.policies;
  }

  /**
   * Instantiate a fragment tree for one response. The same fragment node may
   * appear at multiple positions; every occurrence gets an independent
   * positioned identity and its own response-bound nonce.
   */
  instantiate(root: FragmentNode, options: SynthesisOptions = {}): PositionedGraph {
    const scripts = new Map<string, PositionedScript>();
    const grants: AuthorizationGrant[] = [];
    const frames: Frame[] = [{ fragment: root, path: [], inherited: undefined }];
    const withhold = new Set(options.withholdNonces ?? []);

    while (frames.length > 0) {
      const frame = frames.pop()!;
      const positionKey = keyOf(frame.path);
      for (const script of frame.fragment.scripts) {
        const key = positionKey === '' ? script.id : `${positionKey}::${script.id}`;
        const commitment = sha256(script.bytes);
        const parentInheritedSources = [...(frame.inherited ?? [])];
        const inheritableSources = [...(script.inheritableSources ?? [])];
        const source = inheritableSources[0] ?? { scheme: 'https' as const, host: 'inline.invalid' };
        const nonce = this.authority.mint({ positionKey: key, scriptId: script.id, commitment });
        if (options.publishNonces && !withhold.has(nonce)) {
          this.authority.publish(nonce);
        }
        scripts.set(key, {
          key,
          scriptId: script.id,
          fragmentId: frame.fragment.id,
          positionPath: frame.path,
          commitment,
          source,
          inheritableSources,
          parentInheritedSources,
          nonce,
        });
        grants.push({
          responseId: this.authority.responseId,
          positionKey: key,
          scriptId: script.id,
          commitment,
          inheritableSources,
          nonce,
        });
      }
      for (const [slotName, slotRef] of Object.entries(frame.fragment.slots ?? {})) {
        frames.push({
          fragment: slotRef.fragment,
          path: [...frame.path, slotName],
          inherited: slotRef.inheritSources,
        });
      }
    }

    const edges: LoadEdge[] = [];
    const outgoing = new Map<string, LoadEdge[]>();
    const visitedPositions = new Set<string>();
    const walk = (fragment: FragmentNode, path: string[]): void => {
      const positionKey = keyOf(path);
      for (const spec of fragment.loads) {
        const fromKey = positionKey === '' ? spec.fromScriptId : `${positionKey}::${spec.fromScriptId}`;
        const targetPath = spec.slot === '.' ? path : [...path, spec.slot];
        const toKey =
          keyOf(targetPath) === ''
            ? spec.targetScriptId
            : `${keyOf(targetPath)}::${spec.targetScriptId}`;
        if (!scripts.has(fromKey)) {
          throw new Error(`Load rule in ${fragment.id} references unknown loader ${fromKey}`);
        }
        if (!scripts.has(toKey)) {
          throw new Error(`Load rule in ${fragment.id} targets unknown script ${toKey}`);
        }
        const edge: LoadEdge = {
          id: `${fromKey}->${toKey}`,
          fromKey,
          toKey,
          spec,
          gate: this.bdd.compile(spec.gate),
        };
        edges.push(edge);
        const list = outgoing.get(fromKey) ?? [];
        list.push(edge);
        outgoing.set(fromKey, list);
      }
      for (const [slotName, slotRef] of Object.entries(fragment.slots ?? {})) {
        const childPath = [...path, slotName];
        const childPosition = keyOf(childPath);
        if (visitedPositions.has(childPosition)) {
          throw new Error(`Cyclic slot containment at ${childPosition}; loading cycles must use slot "."`);
        }
        visitedPositions.add(childPosition);
        walk(slotRef.fragment, childPath);
      }
    };
    visitedPositions.add('');
    walk(root, []);

    this.graph = { scripts, edges, outgoing };
    this.grants = grants;
    return this.graph;
  }

  getGrants(): AuthorizationGrant[] {
    return this.grants.map((grant) => ({ ...grant, inheritableSources: [...grant.inheritableSources] }));
  }

  /** Evaluate every independent policy and intersect their full decisions. */
  decide(targetKey: string): TargetDecision {
    const graph = this.requireGraph();
    if (!graph.scripts.has(targetKey)) {
      throw new Error(`Unknown target script ${targetKey}`);
    }
    const evaluations = this.policies.map((policy) => this.evaluatePolicy(policy, graph));
    const perPolicy: Record<string, BDDNode> = {};
    const denials: DenialReason[] = [];

    evaluations.forEach((evaluation, index) => {
      const policy = this.policies[index];
      perPolicy[policy.id] = this.policyTargetCondition(evaluation, targetKey);
      if (this.bdd.isUnsat(perPolicy[policy.id])) {
        denials.push(...evaluation.denials, ...this.orphanCycleDenials(policy, evaluation, graph, targetKey));
      }
    });

    // Intersection of full policy decisions with provenance: a target is
    // allowed only when the same root-initiated chain is feasible under every
    // policy on the same symbolic switch condition. Unioning source sets would
    // lose the root provenance that strict-dynamic requires.
    const allowedCondition = this.intersectDecisions(evaluations, targetKey);
    const shortestChain = this.bdd.isSat(allowedCondition)
      ? this.shortestChain(targetKey, allowedCondition, graph)
      : undefined;
    return {
      targetKey,
      allowedCondition,
      perPolicy,
      allowed: this.bdd.isSat(allowedCondition),
      shortestChain,
      denials,
    };
  }

  private policyTargetCondition(evaluation: PolicyEvaluation, targetKey: string): BDDNode {
    let condition = this.bdd.FALSE;
    for (const tag of evaluation.tags.values()) {
      if (tag.scriptKey === targetKey) {
        condition = this.bdd.or(condition, tag.condition);
      }
    }
    return condition;
  }

  private intersectDecisions(evaluations: PolicyEvaluation[], targetKey: string): BDDNode {
    if (this.policies.length === 0) {
      return this.bdd.FALSE;
    }
    const byRoot = new Map<string, BDDNode[]>();
    evaluations.forEach((evaluation) => {
      const perRoot = new Map<string, BDDNode>();
      for (const tag of evaluation.tags.values()) {
        if (tag.scriptKey !== targetKey) {
          continue;
        }
        perRoot.set(tag.rootId, this.bdd.or(perRoot.get(tag.rootId) ?? this.bdd.FALSE, tag.condition));
      }
      for (const [rootId, condition] of perRoot) {
        const list = byRoot.get(rootId) ?? [];
        list.push(condition);
        byRoot.set(rootId, list);
      }
    });

    let result = this.bdd.FALSE;
    for (const conditions of byRoot.values()) {
      if (conditions.length === evaluations.length) {
        result = this.bdd.or(result, this.bdd.andAll(conditions));
      }
    }
    return result;
  }

  /** Intersection restricted to genuine trusted-root seeds (not propagated tags). */
  private intersectRootsAt(evaluations: PolicyEvaluation[], key: string): BDDNode {
    if (this.policies.length === 0) {
      return this.bdd.FALSE;
    }
    const byRoot = new Map<string, BDDNode[]>();
    evaluations.forEach((evaluation) => {
      const perRoot = new Map<string, BDDNode>();
      for (const tag of evaluation.tags.values()) {
        if (!tag.isRoot || tag.scriptKey !== key) {
          continue;
        }
        perRoot.set(tag.rootId, this.bdd.or(perRoot.get(tag.rootId) ?? this.bdd.FALSE, tag.condition));
      }
      for (const [rootId, condition] of perRoot) {
        const list = byRoot.get(rootId) ?? [];
        list.push(condition);
        byRoot.set(rootId, list);
      }
    });
    let result = this.bdd.FALSE;
    for (const conditions of byRoot.values()) {
      if (conditions.length === evaluations.length) {
        result = this.bdd.or(result, this.bdd.andAll(conditions));
      }
    }
    return result;
  }

  /**
   * Acceptable rendering configurations for a cached fragment occurrence.
   * Each config pins the exact content commitment and nonce binding, so any
   * byte change invalidates the corresponding authorization.
   */
  renderConfigsFor(positionKey: string): SealedRenderConfig[] {
    const graph = this.requireGraph();
    const configs: SealedRenderConfig[] = [];
    for (const script of graph.scripts.values()) {
      if (script.key !== positionKey && !script.key.startsWith(`${positionKey}::`)) {
        continue;
      }
      const decision = this.decide(script.key);
      if (!decision.allowed) {
        continue;
      }
      configs.push({
        responseId: this.authority.responseId,
        positionKey: script.key,
        scriptId: script.scriptId,
        commitment: script.commitment,
        nonce: script.nonce,
        inheritableSources: script.inheritableSources.map((source) => ({ ...source })),
        acceptableWhen: decision.allowedCondition,
      });
    }
    return configs;
  }

  private requireGraph(): PositionedGraph {
    if (!this.graph) {
      throw new Error('Call instantiate() before taking policy decisions');
    }
    return this.graph;
  }

  /**
   * A load is acceptable when its source is admitted by either the parent
   * slot's position-specific inheritance or the target fragment's own
   * inheritable child-script sources. Same-fragment (".") edges therefore keep
   * using the fragment's own declarations, while reused occurrences can be
   * narrowed or widened independently per tree position.
   */
  private loadSourceAllowed(edge: LoadEdge, target: PositionedScript): boolean {
    const inSlot = edge.spec.slot !== '.';
    const candidates = inSlot ? target.parentInheritedSources : target.inheritableSources;
    return candidates.some((source) => sourceMatches(source, edge.spec.source));
  }

  private evaluatePolicy(policy: CspPolicy, graph: PositionedGraph): PolicyEvaluation {
    const tags = new Map<string, TrustTag>();
    const denials: DenialReason[] = [];

    // Seed one trust tag per admissible trusted root, carrying the root's
    // identity as provenance.
    for (const [rootIndex, root] of policy.trustedRoots.entries()) {
      const rootId = `${policy.id}#${rootIndex}:${root.mode}`;
      for (const script of graph.scripts.values()) {
        const accepted = this.rootAccepts(policy, root, script, denials);
        if (accepted) {
          const gate = this.bdd.compile(root.gate);
          this.mergeTag(tags, {
            rootId,
            scriptKey: script.key,
            condition: gate,
            rootScriptId: script.scriptId,
            isRoot: true,
          });
        }
      }
    }

    // Least fixed point per provenance tag over the cyclic loading graph.
    let changed = true;
    while (changed) {
      changed = false;
      for (const edge of graph.edges) {
        const target = graph.scripts.get(edge.toKey)!;
        const inherited = this.loadSourceAllowed(edge, target);
        if (!inherited) {
          denials.push({
            policyId: policy.id,
            code: 'source-not-inheritable',
            detail: `${edge.fromKey} cannot load ${edge.toKey} from ${formatSource(edge.spec.source)}`,
          });
          continue;
        }
        for (const parentTag of [...tags.values()]) {
          if (parentTag.scriptKey !== edge.fromKey) {
            continue;
          }
          const candidate = this.bdd.and(parentTag.condition, edge.gate);
          if (candidate === this.bdd.FALSE) {
            continue;
          }
          const key = `${parentTag.rootId}::${edge.toKey}`;
          const previous = tags.get(key)?.condition ?? this.bdd.FALSE;
          const merged = this.bdd.or(previous, candidate);
          if (merged !== previous) {
            this.mergeTag(tags, {
              rootId: parentTag.rootId,
              scriptKey: edge.toKey,
              condition: merged,
              rootScriptId: parentTag.rootScriptId,
              isRoot: false,
            });
            changed = true;
          }
        }
      }
    }

    return { tags, denials: dedupeDenials(denials) };
  }

  private mergeTag(tags: Map<string, TrustTag>, tag: TrustTag): void {
    const key = `${tag.rootId}::${tag.scriptKey}`;
    const existing = tags.get(key);
    if (existing) {
      existing.condition = this.bdd.or(existing.condition, tag.condition);
    } else {
      tags.set(key, { ...tag });
    }
  }

  private rootAccepts(
    policy: CspPolicy,
    root: CspPolicy['trustedRoots'][number],
    script: PositionedScript,
    denials: DenialReason[],
  ): boolean {
    if (root.mode === 'nonce' && root.scriptId !== script.scriptId) {
      return false;
    }
    if (root.mode === 'host' && !script.inheritableSources.some((source) => sourceMatches(source, root.source))) {
      return false;
    }
    if (root.mode === 'nonce') {
      const result = this.authority.verify(script.nonce, {
        responseId: this.authority.responseId,
        positionKey: script.key,
        scriptId: script.scriptId,
        commitment: script.commitment,
      });
      if (!result.ok) {
        denials.push({
          policyId: policy.id,
          code: result.reason as DenialCode,
          detail: `nonce root for ${script.key} rejected (${result.reason})`,
        });
        return false;
      }
      return true;
    }
    if (root.mode === 'hash') {
      if (root.scriptId !== script.scriptId || root.commitment !== script.commitment) {
        denials.push({
          policyId: policy.id,
          code: 'commitment-mismatch',
          detail: `hash root for ${root.scriptId}@${root.commitment} does not match ${script.key} (${script.commitment})`,
        });
        return false;
      }
      return true;
    }
    if (policy.strictDynamic) {
      denials.push({
        policyId: policy.id,
        code: 'host-only-under-strict-dynamic',
        detail: `host match for ${script.key} cannot bootstrap trust under strict-dynamic`,
      });
      return false;
    }
    return true;
  }

  private shortestChain(
    targetKey: string,
    allowedCondition: BDDNode,
    graph: PositionedGraph,
  ): AuthorizationHop[] | undefined {
    interface QueueFrame {
      key: string;
      condition: BDDNode;
      hops: AuthorizationHop[];
    }
    const queue: QueueFrame[] = [];
    const bestCondition = new Map<string, BDDNode>();
    const evaluations = this.policies.map((policy) => this.evaluatePolicy(policy, graph));

    for (const script of graph.scripts.values()) {
      const rootCondition = this.intersectRootsAt(evaluations, script.key);
      const feasible = this.bdd.and(rootCondition, allowedCondition);
      if (feasible === this.bdd.FALSE) {
        continue;
      }
      const rootHop: AuthorizationHop = { kind: 'root', scriptId: script.scriptId, positionKey: script.key };
      if (script.key === targetKey) {
        return [rootHop];
      }
      bestCondition.set(script.key, feasible);
      queue.push({ key: script.key, condition: feasible, hops: [rootHop] });
    }

    while (queue.length > 0) {
      const frame = queue.shift()!;
      for (const edge of graph.outgoing.get(frame.key) ?? []) {
        const target = graph.scripts.get(edge.toKey)!;
        const inherited = this.loadSourceAllowed(edge, target);
        if (!inherited) {
          continue;
        }
        const feasible = this.bdd.andAll([frame.condition, edge.gate, allowedCondition]);
        if (feasible === this.bdd.FALSE) {
          continue;
        }
        const prior = bestCondition.get(edge.toKey);
        if (prior !== undefined && this.bdd.entails(feasible, prior)) {
          continue;
        }
        bestCondition.set(edge.toKey, prior === undefined ? feasible : this.bdd.or(prior, feasible));
        const hops: AuthorizationHop[] = [
          ...frame.hops,
          {
            kind: 'load',
            scriptId: target.scriptId,
            positionKey: target.key,
            viaSlot: edge.spec.slot,
            edgeGate: edge.spec.gate,
            source: edge.spec.source,
          },
        ];
        if (edge.toKey === targetKey) {
          return hops;
        }
        queue.push({ key: edge.toKey, condition: feasible, hops });
      }
    }
    return undefined;
  }

  /**
   * When a target stays untrusted, flag strongly connected components that are
   * cut off from every root: an orphan cycle left after a trusted root is
   * revoked. A self-loop is also an SCC.
   */
  private orphanCycleDenials(
    policy: CspPolicy,
    evaluation: PolicyEvaluation,
    graph: PositionedGraph,
    targetKey: string,
  ): DenialReason[] {
    const reachable = this.reachableFromRoots(evaluation, graph);
    const indexOf = new Map<string, number>();
    const low = new Map<string, number>();
    const stack: string[] = [];
    const onStack = new Set<string>();
    let counter = 0;
    const denials: DenialReason[] = [];

    const connect = (key: string): void => {
      indexOf.set(key, counter);
      low.set(key, counter);
      counter += 1;
      stack.push(key);
      onStack.add(key);
      for (const edge of graph.outgoing.get(key) ?? []) {
        if (!indexOf.has(edge.toKey)) {
          connect(edge.toKey);
          low.set(key, Math.min(low.get(key)!, low.get(edge.toKey)!));
        } else if (onStack.has(edge.toKey)) {
          low.set(key, Math.min(low.get(key)!, indexOf.get(edge.toKey)!));
        }
      }
      if (low.get(key) === indexOf.get(key)) {
        const component: string[] = [];
        let member: string;
        do {
          member = stack.pop()!;
          onStack.delete(member);
          component.push(member);
        } while (member !== key);
        const cyclic =
          component.length > 1 ||
          (graph.outgoing.get(key) ?? []).some((edge) => edge.toKey === key);
        const orphan = component.every((memberKey) => !reachable.has(memberKey));
        if (cyclic && orphan && component.includes(targetKey)) {
          denials.push({
            policyId: policy.id,
            code: 'orphan-cycle-no-root',
            detail: `SCC [${component.join(', ')}] has no path from a trusted root`,
          });
        }
      }
    };

    for (const key of graph.scripts.keys()) {
      if (!indexOf.has(key)) {
        connect(key);
      }
    }
    return denials;
  }

  private reachableFromRoots(evaluation: PolicyEvaluation, graph: PositionedGraph): Set<string> {
    const reachable = new Set<string>();
    const queue: string[] = [];
    for (const tag of evaluation.tags.values()) {
      if (tag.condition !== this.bdd.FALSE) {
        const key = tag.scriptKey;
        if (reachable.has(key)) {
          continue;
        }
        reachable.add(key);
        queue.push(key);
      }
    }
    while (queue.length > 0) {
      const key = queue.shift()!;
      for (const edge of graph.outgoing.get(key) ?? []) {
        if (!reachable.has(edge.toKey)) {
          reachable.add(edge.toKey);
          queue.push(edge.toKey);
        }
      }
    }
    return reachable;
  }
}

function keyOf(path: string[]): string {
  return path.join('/');
}

function formatSource(source: SourceExpr): string {
  return `${source.scheme}://${source.host}${source.pathPrefix ?? ''}`;
}

function dedupeDenials(denials: DenialReason[]): DenialReason[] {
  const seen = new Set<string>();
  return denials.filter((denial) => {
    const key = `${denial.policyId}:${denial.code}:${denial.detail}`;
    if (seen.has(key)) {
      return false;
    }
    seen.add(key);
    return true;
  });
}
