import { makeError, patternMatches } from './locations.ts';
import { originalSubtreeIds } from './tree-index.ts';
import type {
  AcceptanceError,
  DocId,
  Evidence,
  Facet,
  FacetDecision,
  Grant,
  MergeInput,
  NodeId,
  ProtectedKeyRule,
  SigId,
  Signature,
  TreeDoc,
} from './types.ts';

export interface FacetRequest {
  facet: Facet;
  pointer: string;
  issuer: string;
  /** Primary signature cited by the attribution; must belong to issuer. */
  sigId?: SigId;
  /** Original node the primary signature is expected to cover. */
  originNodeId?: NodeId;
  /**
   * Scope anchor for the primary signature. Defaults to `originNodeId`.
   * Pass `null` to validate the primary signature without subtree scoping
   * (directive / deployment signatures).
   */
  scopeNodeId?: NodeId | null;
  /** Additional content evidence (e.g. template author signature). */
  evidence?: { sigId: SigId; originNodeId: NodeId }[];
  labels?: Record<string, string>;
  via?: string[];
  branchId?: string;
  requireEvidence: boolean;
}

export class Authority {
  readonly signatures = new Map<SigId, Signature>();
  readonly revoked = new Set<SigId>();
  readonly docs = new Map<DocId, TreeDoc>();
  private readonly scopeCache = new Map<SigId, Set<NodeId>>();
  private readonly input: MergeInput;

  constructor(input: MergeInput) {
    this.input = input;
    for (const sig of input.signatures ?? []) this.signatures.set(sig.sigId, sig);
    for (const item of input.revocations ?? []) {
      if (typeof item === 'string') this.revoked.add(item);
      else this.revoked.add(item.sigId);
    }
    for (const sig of input.signatures ?? []) {
      if (sig.revoked) this.revoked.add(sig.sigId);
    }
    for (const branch of input.branches) this.docs.set(branch.doc.docId, branch.doc);
    for (const tree of input.documents ?? []) this.docs.set(tree.docId, tree);
    for (const [id, tree] of Object.entries(input.templates ?? {})) {
      this.docs.set(id, tree);
      this.docs.set(tree.docId, tree);
    }
  }

  isRevoked(sigId: SigId | undefined): boolean {
    return !!sigId && this.revoked.has(sigId);
  }

  /** The immutable original-node scope of a signature (cached closure). */
  scopeOf(sig: Signature): Set<NodeId> | undefined {
    const cached = this.scopeCache.get(sig.sigId);
    if (cached) return cached;
    const tree = this.docs.get(sig.docId);
    if (!tree) return undefined;
    const scope = originalSubtreeIds(tree, sig.nodeId);
    this.scopeCache.set(sig.sigId, scope);
    return scope;
  }

  /** First signature by `issuer` whose original subtree contains `nodeId`. */
  coveringSignature(issuer: string | undefined, nodeId: NodeId): Signature | undefined {
    if (!issuer) return undefined;
    return (this.input.signatures ?? []).find((sig) => {
      if (sig.issuer !== issuer) return false;
      return this.scopeOf(sig)?.has(nodeId) ?? false;
    });
  }

  /** First registered signature whose original subtree contains `nodeId`. */
  anyCoveringSignature(nodeId: NodeId): Signature | undefined {
    return (this.input.signatures ?? []).find((sig) => this.scopeOf(sig)?.has(nodeId) ?? false);
  }

  grantAllows(issuer: string, facet: Facet, pointer: string, labels: Record<string, string>): Grant | undefined {
    return (this.input.grants ?? []).find((grant) => {
      if (grant.issuer !== '*' && grant.issuer !== issuer) return false;
      if (grant.facets !== '*' && !(grant.facets ?? []).includes(facet)) return false;
      if (!patternMatches(grant.path, pointer)) return false;
      if (grant.tenant !== undefined && grant.tenant !== labels.tenant) return false;
      if (grant.role !== undefined) {
        const roles = Array.isArray(grant.role) ? grant.role : [grant.role];
        if (labels.role === undefined || !roles.includes(labels.role)) return false;
      }
      return true;
    });
  }

  protectedRuleFor(pointer: string): ProtectedKeyRule | undefined {
    return (this.input.protectedKeys ?? []).find((rule) => patternMatches(rule.path, pointer));
  }

  mayDeleteProtected(rule: ProtectedKeyRule, issuer: string): boolean {
    if (rule.owner === issuer) return true;
    if (rule.allowDeleteFrom === '*') return true;
    return (rule.allowDeleteFrom ?? []).includes(issuer);
  }

  private validateSignature(
    sigId: SigId,
    originNodeId: NodeId | undefined,
    pointer: string,
    facet: Facet,
    expectedIssuer?: string,
  ): Evidence {
    const violations: string[] = [];
    const sig = this.signatures.get(sigId);
    if (!sig) {
      violations.push('signature-not-found');
      return { sigId, originNodeId: originNodeId ?? '', valid: false, violations };
    }
    if (this.revoked.has(sigId)) violations.push('signature-revoked');
    if (expectedIssuer !== undefined && sig.issuer !== expectedIssuer) {
      violations.push('signature-issuer-mismatch');
    }
    if (originNodeId !== undefined) {
      const scope = this.scopeOf(sig);
      if (!scope) {
        violations.push('signature-doc-missing');
      } else if (!scope.has(originNodeId)) {
        violations.push('outside-signed-subtree');
      }
    }
    void pointer;
    void facet;
    return { sigId, originNodeId: originNodeId ?? sig.nodeId, valid: violations.length === 0, violations };
  }

  evaluateFacet(req: FacetRequest): FacetDecision {
    const labels = req.labels ?? this.input.labels ?? {};
    const via = req.via ?? [];
    const violations: AcceptanceError[] = [];
    const evidence: Evidence[] = [];

    const grant = this.grantAllows(req.issuer, req.facet, req.pointer, labels);
    if (!grant) {
      violations.push(
        makeError(
          'UNAUTHORIZED_FACET',
          `Issuer "${req.issuer}" is not allowed to decide ${req.facet} at ${req.pointer}`,
          { pointer: req.pointer, ...(req.originNodeId ? { nodeId: req.originNodeId } : {}), facet: req.facet },
          { reason: 'no-location-purpose-grant', issuer: req.issuer, facet: req.facet },
        ),
      );
    }

    if (req.sigId) {
      const primaryScope =
        req.scopeNodeId === undefined ? req.originNodeId : req.scopeNodeId ?? undefined;
      const primary = this.validateSignature(
        req.sigId,
        primaryScope,
        req.pointer,
        req.facet,
        req.issuer,
      );
      evidence.push(primary);
      for (const reason of primary.violations) {
        violations.push(this.signatureError(reason, req.sigId, req.pointer, req.facet, req.originNodeId));
      }
    }

    for (const extra of req.evidence ?? []) {
      const item = this.validateSignature(extra.sigId, extra.originNodeId, req.pointer, req.facet);
      evidence.push(item);
      for (const reason of item.violations) {
        violations.push(this.signatureError(reason, extra.sigId, req.pointer, req.facet, extra.originNodeId));
      }
    }

    const hasValidEvidence = evidence.some((item) => item.valid);
    if (req.requireEvidence && !hasValidEvidence) {
      violations.push(
        makeError(
          'UNAUTHORIZED_FACET',
          `No valid signature evidence for ${req.facet} decision at ${req.pointer}`,
          { pointer: req.pointer, ...(req.originNodeId ? { nodeId: req.originNodeId } : {}), facet: req.facet },
          { reason: 'missing-signature-evidence', issuer: req.issuer, facet: req.facet },
        ),
      );
    }

    return {
      facet: req.facet,
      issuer: req.issuer,
      ...(req.sigId ? { sigId: req.sigId } : {}),
      ...(req.originNodeId ? { originNodeId: req.originNodeId } : {}),
      ...(req.branchId ? { branchId: req.branchId } : {}),
      via,
      evidence,
      authorized: violations.length === 0,
      violations,
    };
  }

  private signatureError(
    reason: string,
    sigId: SigId,
    pointer: string,
    facet: Facet,
    nodeId?: NodeId,
  ): AcceptanceError {
    const location = { pointer, ...(nodeId ? { nodeId } : {}), facet };
    if (reason === 'signature-revoked') {
      return makeError('SIGNATURE_REVOKED', `Signature "${sigId}" has been revoked`, location, { sigId });
    }
    if (reason === 'signature-not-found') {
      return makeError('SIGNATURE_NOT_FOUND', `Signature "${sigId}" is not registered`, location, { sigId });
    }
    if (reason === 'signature-issuer-mismatch') {
      return makeError(
        'SIGNATURE_ISSUER_MISMATCH',
        `Signature "${sigId}" was not issued by the attributed issuer`,
        location,
        { sigId },
      );
    }
    return makeError(
      'SIGNATURE_SCOPE',
      `Node "${nodeId ?? ''}" is outside the original subtree covered by signature "${sigId}"`,
      location,
      { sigId, ...(nodeId ? { nodeId } : {}) },
    );
  }
}
