import type { Authority } from './authority.ts';
import {
  joinPointer,
  makeError,
  parentPointer,
  pointerSegments,
  patternMatches,
} from './locations.ts';
import { indexDoc, type DocIndex } from './tree-index.ts';
import type {
  AcceptanceError,
  AliasRule,
  Branch,
  Evidence,
  ExposureRecord,
  LeafProvenance,
  MergeInput,
  NodeId,
  PublishedLeaf,
  MergeTrace,
} from './types.ts';

interface ScopeEvidence {
  sigId: string;
  originNodeId: NodeId;
}

interface CNode {
  runtimeId: string;
  originId: NodeId;
  kind: 'object' | 'value' | 'null' | 'tombstone';
  value?: unknown;
  edges: CEdge[];
  branchId: string;
  issuer: string;
  sigId?: string;
  scopeNodeId: NodeId | null;
  coEvidence: ScopeEvidence[];
  labels: Record<string, string>;
  via: string[];
}

interface CEdge {
  outKey: string;
  child: CNode;
  aliasRule?: AliasRule;
}

interface Candidate {
  branchId: string;
  priority: number;
  node: CNode;
  aliasRule?: AliasRule;
}

interface WalkContext {
  branch: Branch;
  labels: Record<string, string>;
  siteAuth?: { issuer: string; sigId?: string };
  templateStack: string[];
}

export interface EngineOutcome {
  tree: Record<string, unknown>;
  leaves: PublishedLeaf[];
  provenance: LeafProvenance[];
  errors: AcceptanceError[];
  trace: MergeTrace;
  matchedDeletions: Set<string>;
}

export class MergeEngine {
  readonly errors: AcceptanceError[] = [];
  readonly leaves: PublishedLeaf[] = [];
  readonly provenance: LeafProvenance[] = [];
  readonly matchedDeletions = new Set<string>();
  readonly trace: MergeTrace = { branches: [], expansions: [], aliasApplications: [], exposures: [] };
  private readonly indices = new Map<string, DocIndex>();
  private runtimeSeq = 0;
  private readonly requireEvidence: boolean;
  private readonly input: MergeInput;
  private readonly authority: Authority;

  constructor(
    input: MergeInput,
    authority: Authority,
  ) {
    this.input = input;
    this.authority = authority;
    this.requireEvidence = input.options?.requireSignatureEvidence !== false;
  }

  run(): EngineOutcome {
    const globalLabels = this.input.labels ?? {};
    const active: Branch[] = [];

    for (const branch of [...this.input.branches].sort((a, b) => b.priority - a.priority)) {
      const labels = { ...globalLabels, ...(branch.labels ?? {}) };
      const activeBranch = this.conditionActive(branch, labels);
      this.trace.branches.push({
        branchId: branch.branchId,
        priority: branch.priority,
        active: activeBranch,
        ...(activeBranch ? {} : { reason: 'condition-mismatch' }),
      });
      if (activeBranch) active.push(branch);
    }

    const roots: Candidate[] = [];
    for (const branch of active) {
      const root = this.prepareBranch(branch, globalLabels);
      if (root && root.kind === 'object') {
        roots.push({ branchId: branch.branchId, priority: branch.priority, node: root });
      } else if (root) {
        this.errors.push(
          makeError('INVALID_INPUT', `Branch "${branch.branchId}" root must be an object`, {
            pointer: '',
            nodeId: branch.doc.root,
          }),
        );
      }
    }

    if (this.errors.length > 0) {
      return this.outcome({});
    }

    const tree = this.mergeObject('', roots, []);

    for (const deletion of this.input.deletions ?? []) {
      const key = deletion.id ?? `${deletion.path}::${deletion.branchId ?? deletion.nodeId ?? '*'}`;
      if (!this.matchedDeletions.has(key)) {
        this.errors.push(
          makeError(
            'INVALID_INPUT',
            `Delete directive at ${deletion.path} did not match any contribution`,
            { pointer: deletion.path, facet: 'existence' },
            { reason: 'deletion-target-not-found', issuer: deletion.issuer },
          ),
        );
      }
    }

    return this.outcome(tree);
  }

  private outcome(tree: Record<string, unknown>): EngineOutcome {
    return {
      tree,
      leaves: this.leaves,
      provenance: this.provenance,
      errors: this.errors,
      trace: this.trace,
      matchedDeletions: this.matchedDeletions,
    };
  }

  private conditionActive(branch: Branch, labels: Record<string, string>): boolean {
    if (!branch.when?.labels) return true;
    return Object.entries(branch.when.labels).every(([key, value]) => labels[key] === value);
  }

  private prepareBranch(branch: Branch, globalLabels: Record<string, string>): CNode | undefined {
    return this.walk(branch.doc.root, branch.doc, '', {
      branch,
      labels: { ...globalLabels, ...(branch.labels ?? {}) },
      templateStack: [],
    });
  }

  private newRuntimeId(prefix: string): string {
    this.runtimeSeq += 1;
    return `${prefix}#${this.runtimeSeq}`;
  }

  private walk(nodeId: NodeId, doc: MergeInput['branches'][number]['doc'], pointer: string, ctx: WalkContext): CNode | undefined {
    const node = doc.nodes[nodeId];
    if (!node) {
      this.errors.push(makeError('UNKNOWN_NODE', `Node "${nodeId}" is missing from doc "${doc.docId}"`, { pointer, nodeId }));
      return undefined;
    }

    if (node.kind === 'ref') {
      return this.walkRef(nodeId, node.template, pointer, ctx, node.tenant, node.role, node.attribution?.issuer, node.attribution?.sigId);
    }

    const issuer = ctx.siteAuth?.issuer ?? node.attribution?.issuer ?? ctx.branch.attribution?.issuer ?? 'unattributed';
    const explicitSig = ctx.siteAuth?.sigId ?? node.attribution?.sigId ?? ctx.branch.attribution?.sigId;
    const labels = ctx.labels;
    const isTemplate = ctx.siteAuth !== undefined;

    let sigId = explicitSig;
    let scopeNodeId: NodeId | null = isTemplate ? null : nodeId;
    const coEvidence: ScopeEvidence[] = [];

    if (!isTemplate && !sigId) {
      sigId = this.authority.coveringSignature(issuer, nodeId)?.sigId;
    }
    if (isTemplate) {
      const templateSig =
        node.attribution?.sigId
          ? node.attribution.sigId
          : this.authority.coveringSignature(node.attribution?.issuer, nodeId)?.sigId ??
            this.authority.anyCoveringSignature(nodeId)?.sigId;
      if (templateSig) coEvidence.push({ sigId: templateSig, originNodeId: nodeId });
    }

    const base: CNode = {
      runtimeId: this.newRuntimeId(nodeId),
      originId: nodeId,
      kind: node.kind === 'object' ? 'object' : node.kind,
      ...(node.kind === 'value' ? { value: node.value } : {}),
      edges: [],
      branchId: ctx.branch.branchId,
      issuer,
      ...(sigId ? { sigId } : {}),
      scopeNodeId,
      coEvidence,
      labels,
      via: isTemplate ? ['template'] : [],
    };

    if (node.kind === 'object') {
      const mapped = new Map<string, { edge: (typeof node.edges)[number]; outKey: string; aliasRule?: AliasRule }[]>();
      for (const edge of node.edges) {
        const aliasRule = this.findAlias(edge.key, pointer);
        const outKey = aliasRule?.canonical ?? edge.key;
        const list = mapped.get(outKey) ?? [];
        list.push({ edge, outKey, ...(aliasRule ? { aliasRule } : {}) });
        mapped.set(outKey, list);
        if (aliasRule) {
          this.trace.aliasApplications.push({
            pointer: joinPointer(pointer, outKey),
            from: edge.key,
            to: outKey,
            ruleIssuer: aliasRule.attribution.issuer,
          });
        }
      }

      for (const [outKey, group] of [...mapped.entries()].sort((a, b) => a[0].localeCompare(b[0]))) {
        if (group.length > 1) {
          this.errors.push(
            makeError(
              'ALIAS_COLLISION',
              `Key "${outKey}" has ${group.length} colliding sources under ${pointer || '/'}`,
              { pointer, nodeId, facet: 'name' },
              {
                canonical: outKey,
                sources: group.map((g) => g.edge.key).sort(),
              },
            ),
          );
        }
        for (const item of group) {
          const childPointer = joinPointer(pointer, outKey);
          const child = this.walk(item.edge.child, doc, childPointer, ctx);
          if (child) base.edges.push({ outKey, child, ...(item.aliasRule ? { aliasRule: item.aliasRule } : {}) });
        }
      }
    }

    return base;
  }

  private walkRef(
    refNodeId: NodeId,
    templateId: string,
    pointer: string,
    ctx: WalkContext,
    tenant?: string,
    role?: string,
    refIssuer?: string,
    refSigId?: string,
  ): CNode | undefined {
    if (ctx.templateStack.includes(templateId)) {
      const chain = [...ctx.templateStack, templateId];
      this.errors.push(
        makeError(
          'TEMPLATE_CYCLE',
          `Template recursion cycle: ${chain.join(' -> ')}`,
          { pointer, nodeId: refNodeId },
          { chain },
        ),
      );
      return {
        runtimeId: this.newRuntimeId(refNodeId),
        originId: refNodeId,
        kind: 'tombstone',
        edges: [],
        branchId: ctx.branch.branchId,
        issuer: ctx.siteAuth?.issuer ?? refIssuer ?? ctx.branch.attribution?.issuer ?? 'unattributed',
        scopeNodeId: null,
        coEvidence: [],
        labels: ctx.labels,
        via: ['template', 'cycle'],
      };
    }

    const templateDoc = this.input.templates?.[templateId] ?? this.authority.docs.get(templateId);
    if (!templateDoc) {
      this.errors.push(
        makeError('UNKNOWN_TEMPLATE', `Unknown template reference "${templateId}"`, {
          pointer,
          nodeId: refNodeId,
        }),
        );
      return undefined;
    }

    const siteAuth =
      ctx.siteAuth ?? {
        issuer: refIssuer ?? ctx.branch.attribution?.issuer ?? 'unattributed',
        ...(refSigId ?? ctx.branch.attribution?.sigId
          ? { sigId: refSigId ?? ctx.branch.attribution?.sigId }
          : {}),
      };
    const labels = {
      ...ctx.labels,
      ...(tenant ? { tenant } : {}),
      ...(role ? { role } : {}),
    };

    this.trace.expansions.push({ pointer, template: templateId, instanceRoot: refNodeId });

    return this.walk(templateDoc.root, templateDoc, pointer, {
      ...ctx,
      labels,
      siteAuth,
      templateStack: [...ctx.templateStack, templateId],
    });
  }

  private findAlias(key: string, parentPointer: string): AliasRule | undefined {
    return this.input.aliases?.find((rule) => {
      if (rule.alias !== key) return false;
      if (rule.at === undefined) return true;
      return patternMatches(rule.at, parentPointer);
    });
  }

  private mergeObject(pointer: string, sources: Candidate[], inheritedExposures: ExposureRecord[]): Record<string, unknown> {
    const json: Record<string, unknown> = {};
    const keyed = new Map<string, Candidate[]>();

    for (const source of sources) {
      if (source.node.kind !== 'object') continue;
      for (const edge of source.node.edges) {
        const list = keyed.get(edge.outKey) ?? [];
        list.push({
          branchId: source.branchId,
          priority: source.priority,
          node: edge.child,
          ...(edge.aliasRule ? { aliasRule: edge.aliasRule } : {}),
        });
        keyed.set(edge.outKey, list);
      }
    }

    for (const key of [...keyed.keys()].sort((a, b) => a.localeCompare(b))) {
      const childPointer = joinPointer(pointer, key);
      let candidates = [...(keyed.get(key) ?? [])].sort((a, b) => b.priority - a.priority);
      const exposures = [...inheritedExposures];

      candidates = this.applyDeletions(childPointer, candidates, exposures);

      for (const candidate of candidates) {
        if (candidate.node.kind === 'tombstone') this.guardTombstone(childPointer, candidate.node.issuer);
      }

      const top = candidates[0];
      if (!top) continue;

      if (top.node.kind === 'tombstone') {
        this.authorizeTombstone(childPointer, top.node);
        continue;
      }

      if (top.node.kind === 'object') {
        const objectSources = candidates
          .filter((c) => c.node.kind === 'object')
          .map((c) => ({ branchId: c.branchId, priority: c.priority, node: c.node }));
        json[key] = this.mergeObject(childPointer, objectSources, exposures);
        continue;
      }

      const value = top.node.kind === 'null' ? null : top.node.value;
      json[key] = value as string | number | boolean | readonly (string | number | boolean)[] | null;
      this.emitLeaf(key, childPointer, top, exposures);
    }

    return json;
  }

  private applyDeletions(pointer: string, candidates: Candidate[], exposures: ExposureRecord[]): Candidate[] {
    const directives = (this.input.deletions ?? []).filter((d) => d.path === pointer);
    if (directives.length === 0) return candidates;

    let remaining = [...candidates];
    for (const deletion of directives) {
      const directiveKey = deletion.id ?? `${deletion.path}::${deletion.branchId ?? deletion.nodeId ?? '*'}`;
      let index = -1;
      if (deletion.branchId) {
        index = remaining.findIndex((c) => c.branchId === deletion.branchId);
      } else if (deletion.nodeId) {
        index = remaining.findIndex((c) => c.node.originId === deletion.nodeId);
      } else {
        index = 0;
      }

      if (index === -1) continue;
      this.matchedDeletions.add(directiveKey);
      const removed = remaining[index];
      remaining = remaining.slice(0, index).concat(remaining.slice(index + 1));

      this.guardTombstone(pointer, deletion.issuer);
      this.authorizeDeletion(deletion.issuer, deletion.sigId, pointer, removed.node);

      const exposed = remaining[0];
      const record: ExposureRecord = {
        pointer,
        ...(removed.branchId ? { deletedBranchId: removed.branchId } : {}),
        deletedIssuer: removed.node.issuer,
        ...(removed.node.sigId ? { deletedSigId: removed.node.sigId } : {}),
        deletedSignatureRevoked: this.signatureRevokedOn(removed.node),
        ...(exposed ? { exposedBranchId: exposed.branchId } : {}),
        ...(exposed ? { exposedIssuer: exposed.node.issuer } : {}),
        ...(exposed?.node.sigId ? { exposedSigId: exposed.node.sigId } : {}),
        exposedSignatureRevoked: exposed ? this.signatureRevokedOn(exposed.node) : false,
      };
      if (exposed && exposed.branchId !== removed.branchId) {
        exposures.push(record);
        this.trace.exposures.push(record);
      }
    }

    return remaining;
  }

  private signatureRevokedOn(node: CNode): boolean {
    if (node.sigId && this.authority.isRevoked(node.sigId)) return true;
    return node.coEvidence.some((evidence) => this.authority.isRevoked(evidence.sigId));
  }

  private guardTombstone(pointer: string, issuer: string): void {
    const rule = this.authority.protectedRuleFor(pointer);
    if (rule && !this.authority.mayDeleteProtected(rule, issuer)) {
      this.errors.push(
        makeError(
          'CROSS_SCOPE_TOMBSTONE',
          `Issuer "${issuer}" (scope) cannot clear protected key ${pointer} owned by "${rule.owner}"`,
          { pointer, facet: 'existence' },
          { issuer, owner: rule.owner },
        ),
      );
    }
  }

  private authorizeTombstone(pointer: string, node: CNode): void {
    const decision = this.authority.evaluateFacet({
      facet: 'existence',
      pointer,
      issuer: node.issuer,
      ...(node.sigId ? { sigId: node.sigId } : {}),
      originNodeId: node.originId,
      scopeNodeId: node.scopeNodeId,
      evidence: node.coEvidence,
      labels: node.labels,
      branchId: node.branchId,
      via: ['tombstone'],
      requireEvidence: this.requireEvidence,
    });
    this.errors.push(...decision.violations);
  }

  private authorizeDeletion(issuer: string, sigId: string | undefined, pointer: string, removed: CNode): void {
    const contentEvidence: Evidence[] = this.contentEvidenceOf(removed);
    const decision = this.authority.evaluateFacet({
      facet: 'existence',
      pointer,
      issuer,
      ...(sigId ? { sigId } : {}),
      originNodeId: removed.originId,
      scopeNodeId: null,
      evidence: contentEvidence.map((e) => ({ sigId: e.sigId, originNodeId: e.originNodeId })),
      labels: removed.labels,
      branchId: removed.branchId,
      via: ['delete'],
      requireEvidence: this.requireEvidence,
    });
    this.errors.push(...decision.violations);
  }

  private contentEvidenceOf(node: CNode): Evidence[] {
    const items: Evidence[] = node.coEvidence.map((e) => ({
      sigId: e.sigId,
      originNodeId: e.originNodeId,
      valid: true,
      violations: [],
    }));
    if (node.sigId && node.scopeNodeId) {
      items.push({ sigId: node.sigId, originNodeId: node.scopeNodeId, valid: true, violations: [] });
    }
    return items;
  }

  private emitLeaf(key: string, pointer: string, candidate: Candidate, exposures: ExposureRecord[]): void {
    const node = candidate.node;
    const via = this.detectVia(node, pointer, candidate.aliasRule);
    const common = {
      pointer,
      labels: node.labels,
      branchId: node.branchId,
      requireEvidence: this.requireEvidence,
    } as const;

    const nameRequest = candidate.aliasRule
      ? {
          ...common,
          facet: 'name' as const,
          issuer: candidate.aliasRule.attribution.issuer,
          ...(candidate.aliasRule.attribution.sigId ? { sigId: candidate.aliasRule.attribution.sigId } : {}),
          originNodeId: node.originId,
          scopeNodeId: null,
          evidence: this.contentEvidenceOf(node).map((e) => ({ sigId: e.sigId, originNodeId: e.originNodeId })),
          via: [...via, 'alias-decision'],
        }
      : {
          ...common,
          facet: 'name' as const,
          issuer: node.issuer,
          ...(node.sigId ? { sigId: node.sigId } : {}),
          originNodeId: node.originId,
          scopeNodeId: node.scopeNodeId,
          evidence: node.coEvidence,
          via,
        };

    const valueDecision = this.authority.evaluateFacet({
      ...common,
      facet: 'value',
      issuer: node.issuer,
      ...(node.sigId ? { sigId: node.sigId } : {}),
      originNodeId: node.originId,
      scopeNodeId: node.scopeNodeId,
      evidence: node.coEvidence,
      via,
    });
    const nameDecision = this.authority.evaluateFacet(nameRequest);
    const parentDecision = this.authority.evaluateFacet({
      ...common,
      facet: 'parent',
      issuer: node.issuer,
      ...(node.sigId ? { sigId: node.sigId } : {}),
      originNodeId: node.originId,
      scopeNodeId: node.scopeNodeId,
      evidence: node.coEvidence,
      via,
    });
    const existenceDecision = this.authority.evaluateFacet({
      ...common,
      facet: 'existence',
      issuer: node.issuer,
      ...(node.sigId ? { sigId: node.sigId } : {}),
      originNodeId: node.originId,
      scopeNodeId: node.scopeNodeId,
      evidence: node.coEvidence,
      via,
    });

    const decisions = [nameDecision, valueDecision, parentDecision, existenceDecision];
    for (const decision of decisions) this.errors.push(...decision.violations);

    const value = node.kind === 'null' ? null : (node.value as PublishedLeaf['value']);
    const authorized = decisions.every((d) => d.authorized);

    const provenance: LeafProvenance = {
      pointer,
      key,
      value,
      kind: node.kind === 'null' ? 'null' : 'value',
      name: nameDecision,
      valueDecision,
      parent: parentDecision,
      existence: existenceDecision,
      exposures,
      authorized,
    };

    this.provenance.push(provenance);
    this.leaves.push({ pointer, key, value, provenance });
  }

  private detectVia(node: CNode, pointer: string, aliasRule?: AliasRule): string[] {
    const via = [...node.via];
    if (aliasRule && !via.includes('alias')) via.push('alias');
    if (!via.includes('template') && node.sigId && node.scopeNodeId) {
      const sig = this.authority.signatures.get(node.sigId);
      const doc = sig ? this.authority.docs.get(sig.docId) : undefined;
      if (sig && doc) {
        let index = this.indices.get(doc.docId);
        if (!index) {
          index = indexDoc(doc);
          this.indices.set(doc.docId, index);
        }
        const originalPointer = index.pointerOf.get(node.scopeNodeId);
        if (originalPointer !== undefined && originalPointer !== pointer) {
          const originalParent = parentPointer(originalPointer);
          const outputParent = parentPointer(pointer);
          if (originalParent !== outputParent && !via.includes('moved')) via.push('moved');
          const originalKey = pointerSegments(originalPointer).at(-1);
          const outputKey = pointerSegments(pointer).at(-1);
          if (originalKey !== outputKey && !via.includes('renamed')) via.push('renamed');
        }
      }
    }
    return via;
  }
}
