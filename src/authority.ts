import { randomUUID } from "node:crypto";
import type {
  Action,
  Attributes,
  AuthzRequest,
  CheckFailure,
  Grant,
  Policy,
  Principal,
  Proof,
  ProofLink,
  Resource,
  SignedGrant,
  VerifyResult,
} from "./types.ts";
import { DelegationError } from "./errors.ts";
import { KeyDirectory } from "./keys.ts";
import type { Clock } from "./clock.ts";
import { and, evalPolicy, validatePolicy } from "./policy.ts";
import { canonicalize } from "./canonical.ts";

interface Scope {
  resources: ReadonlyArray<Resource>;
  actions: ReadonlyArray<Action>;
}

export interface RootOptions extends Scope {
  subject: Principal;
  maxDepth: number;
  sealed?: boolean;
  durationSeconds?: number;
  notBefore?: number;
  policy?: Policy;
  id?: string;
}

export interface DelegateOptions extends Scope {
  subject: Principal;
  sealed?: boolean;
  durationSeconds?: number;
  /** Defaults to the parent grant's depth minus one. */
  maxDepth?: number;
  notBefore?: number;
  policy?: Policy;
  id?: string;
}

type FailureCode = CheckFailure["code"];

function failure(code: FailureCode, grantId: string | null, detail: string): CheckFailure {
  return { code, grantId, detail };
}

function sortedUnique(values: ReadonlyArray<string>): string[] {
  return Array.from(new Set(values)).sort();
}

function subsetOf(child: ReadonlyArray<string>, parent: ReadonlyArray<string>): boolean {
  const parentSet = new Set(parent);
  return child.every((value) => parentSet.has(value));
}

/**
 * Order signed links into a root-first chain, validating structural linkage.
 * Cycles (including repeated principals) are rejected here, so a loop of
 * delegations can never present itself as a source of authority.
 */
export function orderChain(links: ReadonlyArray<ProofLink>): {
  chain: ProofLink[];
  failures: CheckFailure[];
} {
  const failures: CheckFailure[] = [];
  if (links.length === 0) {
    return { chain: [], failures: [failure("malformed", null, "empty proof chain")] };
  }

  const byId = new Map<string, ProofLink>();
  for (const link of links) {
    if (!link || typeof link.sig !== "string" || !link.grant || typeof link.grant.id !== "string") {
      failures.push(failure("malformed", null, "link is not a signed grant"));
      continue;
    }
    if (byId.has(link.grant.id)) {
      failures.push(failure("cycle", link.grant.id, "grant id appears more than once in chain"));
      continue;
    }
    byId.set(link.grant.id, link);
  }
  if (failures.length > 0) return { chain: [], failures };

  const roots = Array.from(byId.values()).filter((link) => link.grant.parentId === null);
  if (roots.length !== 1) {
    return {
      chain: [],
      failures: [
        failure(
          "anchor",
          null,
          roots.length === 0 ? "chain has no root grant" : "chain has multiple root grants",
        ),
      ],
    };
  }

  // The presented order is part of the proof: the first link must be the root
  // and every following link must be a child of its predecessor. Reordering a
  // captured proof cannot make it verify.
  if (links[0] !== roots[0]) {
    return { chain: [], failures: [failure("linkage", null, "proof chain does not begin at its root")] };
  }
  const chain: ProofLink[] = [links[0]];
  const seenSubjects = new Set<Principal>([links[0].grant.subject]);
  for (let index = 1; index < links.length; index += 1) {
    const previous = links[index - 1];
    const current = links[index];
    const grant = current.grant;
    if (grant.parentId !== previous.grant.id) {
      return {
        chain: [],
        failures: [failure("linkage", grant.id, `link ${index} does not follow its predecessor in the chain`)],
      };
    }
    if (grant.issuer !== previous.grant.subject) {
      return {
        chain: [],
        failures: [
          failure("linkage", grant.id, `issuer ${grant.issuer} is not the subject of the parent grant`),
        ],
      };
    }
    if (seenSubjects.has(grant.subject)) {
      return { chain: [], failures: [failure("cycle", grant.id, `subject ${grant.subject} repeats in chain`)] };
    }
    seenSubjects.add(grant.subject);
    chain.push(current);
  }
  return { chain, failures: [] };
}

export class DelegationAuthority {
  private readonly grants = new Map<string, SignedGrant>();
  private readonly revoked = new Set<string>();
  private readonly keys: KeyDirectory;
  private readonly clock: Clock;

  constructor(keys: KeyDirectory, clock: Clock) {
    this.keys = keys;
    this.clock = clock;
  }

  isRevoked(grantId: string): boolean {
    return this.revoked.has(grantId);
  }

  get(grantId: string): SignedGrant | undefined {
    return this.grants.get(grantId);
  }

  /** Create a self-signed root grant: the only allowed source of power. */
  issueRoot(owner: Principal, options: RootOptions): SignedGrant {
    const now = this.clock.now();
    const resources = this.requireNonEmptyScope(options.resources, "resources");
    const actions = this.requireNonEmptyScope(options.actions, "actions");
    if (!Number.isInteger(options.maxDepth) || options.maxDepth < 0) {
      throw new DelegationError("malformed", null, "maxDepth must be a non-negative integer");
    }
    const policy = options.policy ?? { kind: "allowAll" } satisfies Policy;
    validatePolicy(policy);
    const notBefore = options.notBefore ?? now;
    const expiresAt = this.resolveExpiry(notBefore, options.durationSeconds);
    const grant: Grant = {
      id: options.id ?? randomUUID(),
      parentId: null,
      issuer: owner,
      subject: options.subject,
      resources: sortedUnique(resources),
      actions: sortedUnique(actions),
      maxDepth: options.maxDepth,
      sealed: options.sealed ?? false,
      notBefore,
      expiresAt,
      policy,
      parentSig: null,
    };
    if (this.grants.has(grant.id)) {
      throw new DelegationError("malformed", grant.id, "grant id already exists");
    }
    const sig = this.signGrant(owner, grant);
    const signed: SignedGrant = { grant, sig };
    this.grants.set(grant.id, signed);
    return signed;
  }

  /**
   * Delegate within the issuer's own grant. Rejects scope expansion, exceeding
   * remaining depth, sealed redelegation, cycle formation (including return
   * delegation to any ancestor principal), and lifetime escape.
   */
  delegate(issuer: Principal, parentId: string, options: DelegateOptions): SignedGrant {
    const parent = this.grants.get(parentId);
    if (!parent) {
      throw new DelegationError("malformed", parentId, "parent grant not found");
    }

    const ancestorLinks = this.linksToRoot(parentId);
    const parentCheck = this.inspectChain(ancestorLinks, { at: this.clock.now(), usageCheck: false });
    if (parentCheck.length > 0) {
      throw new DelegationError(parentCheck[0].code, parentCheck[0].grantId, parentCheck[0].detail);
    }

    const parentGrant = parent.grant;
    if (parentGrant.subject !== issuer) {
      throw new DelegationError(
        "subject",
        parentId,
        `issuer ${issuer} is not the subject of parent grant`,
      );
    }
    if (parentGrant.sealed) {
      throw new DelegationError("sealed", parentId, "parent grant is sealed (redelegation forbidden)");
    }
    if (parentGrant.maxDepth <= 0) {
      throw new DelegationError("depth", parentId, "parent grant has no remaining delegation depth");
    }
    if (options.subject === issuer) {
      throw new DelegationError("cycle", null, "cannot delegate to self");
    }
    const ancestorSubjects = ancestorLinks.map((link) => link.grant.subject);
    if (ancestorSubjects.includes(options.subject)) {
      throw new DelegationError(
        "cycle",
        null,
        `subject ${options.subject} already appears in the delegation chain`,
      );
    }

    const resources = this.requireNonEmptyScope(options.resources, "resources");
    const actions = this.requireNonEmptyScope(options.actions, "actions");
    if (!subsetOf(resources, parentGrant.resources)) {
      throw new DelegationError("nesting", parentId, "child resources are not a subset of parent resources");
    }
    if (!subsetOf(actions, parentGrant.actions)) {
      throw new DelegationError("nesting", parentId, "child actions are not a subset of parent actions");
    }

    const now = this.clock.now();
    const notBefore = options.notBefore ?? now;
    if (notBefore < parentGrant.notBefore) {
      throw new DelegationError("time", parentId, "child notBefore precedes parent notBefore");
    }
    const expiresAt = this.resolveExpiry(notBefore, options.durationSeconds);
    if (expiresAt > parentGrant.expiresAt) {
      throw new DelegationError("time", parentId, "child would outlive parent grant");
    }

    const maxDepth = options.maxDepth ?? parentGrant.maxDepth - 1;
    if (!Number.isInteger(maxDepth) || maxDepth < 0) {
      throw new DelegationError("depth", null, "maxDepth must be a non-negative integer");
    }
    if (maxDepth > parentGrant.maxDepth - 1) {
      throw new DelegationError(
        "depth",
        parentId,
        `depth ${maxDepth} exceeds remaining depth ${parentGrant.maxDepth - 1}`,
      );
    }

    const policy = and(parentGrant.policy, options.policy ?? { kind: "allowAll" });
    validatePolicy(policy);

    const grant: Grant = {
      id: options.id ?? randomUUID(),
      parentId: parentGrant.id,
      issuer,
      subject: options.subject,
      resources: sortedUnique(resources),
      actions: sortedUnique(actions),
      maxDepth,
      sealed: options.sealed ?? false,
      notBefore,
      expiresAt,
      policy,
      parentSig: parent.sig,
    };
    if (this.grants.has(grant.id)) {
      throw new DelegationError("malformed", grant.id, "grant id already exists");
    }
    const sig = this.signGrant(issuer, grant);
    const signed: SignedGrant = { grant, sig };
    this.grants.set(grant.id, signed);
    return signed;
  }

  /** Revoke one grant; every descendant is invalidated at verification time. */
  revoke(revoker: Principal, grantId: string): void {
    const signed = this.grants.get(grantId);
    if (!signed) {
      throw new DelegationError("malformed", grantId, "grant not found");
    }
    if (signed.grant.issuer !== revoker) {
      throw new DelegationError(
        "subject",
        grantId,
        `only issuer ${signed.grant.issuer} may revoke this grant`,
      );
    }
    this.revoked.add(grantId);
  }

  /** Build the complete root-to-leaf chain proof for an authorization request. */
  prove(request: AuthzRequest): Proof {
    const leaf = this.grants.get(request.grantId);
    if (!leaf) {
      throw new DelegationError("malformed", request.grantId, "grant not found");
    }
    const links = this.linksToRoot(request.grantId);
    return {
      leafId: request.grantId,
      subject: request.subject,
      resource: request.resource,
      action: request.action,
      attrs: request.attrs,
      at: request.at,
      chain: links.map((link) => ({ grant: link.grant, sig: link.sig })),
    };
  }

  /**
   * Verify a full chain proof using only the proof contents, the local
   * revocation set, and locally registered public keys. Nothing external is
   * consulted; a forged, reordered, tampered, expired, revoked, over-scoped,
   * or policy-failing proof yields allowed=false with reasons.
   */
  verifyProof(proof: Proof, options: { bindToStore?: boolean } = {}): VerifyResult {
    const reasons: CheckFailure[] = [];

    if (!proof || typeof proof.leafId !== "string" || !Array.isArray(proof.chain)) {
      return {
        allowed: false,
        reasons: [failure("malformed", null, "proof is malformed")],
        proof,
      };
    }

    const { chain, failures: orderFailures } = orderChain(proof.chain);
    if (orderFailures.length > 0 || chain.length === 0) {
      return { allowed: false, reasons: orderFailures, proof };
    }

    const leaf = chain[chain.length - 1];
    if (leaf.grant.id !== proof.leafId) {
      reasons.push(failure("linkage", leaf.grant.id, "proof leafId does not match chain leaf"));
    }
    if (leaf.grant.subject !== proof.subject) {
      reasons.push(
        failure("subject", leaf.grant.id, `request subject ${proof.subject} is not the leaf holder`),
      );
    }

    for (const link of chain) {
      const grant = link.grant;

      const stored = this.grants.get(grant.id);
      if (options.bindToStore && !stored) {
        reasons.push(failure("linkage", grant.id, "grant is not present in the local store"));
      }
      if (stored && stored.sig !== link.sig) {
        reasons.push(failure("sig", grant.id, "signature differs from stored grant"));
      }
      if (stored && canonicalize(stored.grant) !== canonicalize(grant)) {
        reasons.push(failure("sig", grant.id, "grant contents differ from stored grant"));
      }

      if (!this.keys.has(grant.issuer)) {
        reasons.push(failure("unknownKey", grant.id, `no registered public key for ${grant.issuer}`));
      } else if (!this.keys.verify(grant.issuer, grant, link.sig)) {
        reasons.push(failure("sig", grant.id, "signature does not verify"));
      }

      if (grant.parentId === null) {
        if (grant.parentSig !== null) {
          reasons.push(failure("anchor", grant.id, "root grant must carry a null parentSig"));
        }
        if (grant.issuer !== grant.subject) {
          reasons.push(
            failure("anchor", grant.id, "root grant must be self-issued to its owner subject"),
          );
        }
      } else {
        const parent = chain.find((candidate) => candidate.grant.id === grant.parentId);
        if (!parent) {
          reasons.push(failure("linkage", grant.id, `parent ${grant.parentId} is absent from chain`));
        } else {
          if (grant.parentSig !== parent.sig) {
            reasons.push(failure("linkage", grant.id, "parentSig does not match the parent grant signature"));
          }
          if (!subsetOf(grant.resources, parent.grant.resources)) {
            reasons.push(failure("nesting", grant.id, "resources escape the parent scope"));
          }
          if (!subsetOf(grant.actions, parent.grant.actions)) {
            reasons.push(failure("nesting", grant.id, "actions escape the parent scope"));
          }
          if (grant.maxDepth > parent.grant.maxDepth - 1) {
            reasons.push(failure("depth", grant.id, "delegation depth exceeds the parent allowance"));
          }
          if (parent.grant.sealed) {
            reasons.push(failure("sealed", grant.id, "parent grant is sealed"));
          }
          if (parent.grant.maxDepth < 1) {
            reasons.push(failure("depth", grant.id, "parent grant has no delegation depth left"));
          }
          if (grant.notBefore < parent.grant.notBefore || grant.expiresAt > parent.grant.expiresAt) {
            reasons.push(failure("time", grant.id, "grant lifetime is not contained within the parent lifetime"));
          }
        }
      }

      if (this.revoked.has(grant.id)) {
        reasons.push(failure("revoked", grant.id, "grant has been revoked"));
      }
      if (proof.at < grant.notBefore || proof.at >= grant.expiresAt) {
        reasons.push(failure("time", grant.id, `grant not valid at ${proof.at}`));
      }
    }

    const leafGrant = leaf.grant;
    if (!leafGrant.resources.includes(proof.resource)) {
      reasons.push(failure("requestScope", leafGrant.id, `resource ${proof.resource} is outside the grant`));
    }
    if (!leafGrant.actions.includes(proof.action)) {
      reasons.push(failure("requestScope", leafGrant.id, `action ${proof.action} is outside the grant`));
    }

    if (!evalPolicy(leafGrant.policy, proof.attrs ?? {})) {
      reasons.push(failure("policy", leafGrant.id, "conjoined chain policy denied the request"));
    }

    return { allowed: reasons.length === 0, reasons, proof };
  }

  authorize(request: AuthzRequest): VerifyResult {
    return this.verifyProof(this.prove(request), { bindToStore: true });
  }

  private linksToRoot(grantId: string): ProofLink[] {
    const links: ProofLink[] = [];
    const seen = new Set<string>();
    let currentId: string | null = grantId;
    while (currentId !== null) {
      if (seen.has(currentId)) {
        throw new DelegationError("cycle", currentId, "stored delegation graph contains a cycle");
      }
      seen.add(currentId);
      const signed = this.grants.get(currentId);
      if (!signed) {
        throw new DelegationError("linkage", currentId, "grant is missing from the store");
      }
      links.push({ grant: signed.grant, sig: signed.sig });
      currentId = signed.grant.parentId;
    }
    return links.reverse();
  }

  private inspectChain(
    links: ReadonlyArray<ProofLink>,
    options: { at: number; attrs?: Attributes; resource?: Resource; action?: Action; usageCheck?: boolean },
  ): CheckFailure[] {
    const synthetic: Proof = {
      leafId: links[links.length - 1].grant.id,
      subject: links[links.length - 1].grant.subject,
      resource: options.resource ?? links[links.length - 1].grant.resources[0],
      action: options.action ?? links[links.length - 1].grant.actions[0],
      attrs: options.attrs ?? {},
      at: options.at,
      chain: links,
    };
    const result = this.verifyProof(synthetic);
    if (options.usageCheck === false) {
      return result.reasons.filter((reason) => reason.code !== "policy" && reason.code !== "requestScope");
    }
    return result.reasons;
  }

  private requireNonEmptyScope(values: ReadonlyArray<string>, label: string): string[] {
    if (!Array.isArray(values) || values.length === 0) {
      throw new DelegationError("malformed", null, `${label} must be a non-empty finite set`);
    }
    return sortedUnique(values);
  }

  private resolveExpiry(notBefore: number, durationSeconds?: number): number {
    if (durationSeconds === undefined) {
      return Number.MAX_SAFE_INTEGER;
    }
    if (!Number.isInteger(durationSeconds) || durationSeconds <= 0) {
      throw new DelegationError("time", null, "durationSeconds must be a positive integer");
    }
    return notBefore + durationSeconds;
  }

  private signGrant(issuer: Principal, grant: Grant): string {
    if (!this.keys.has(issuer)) {
      throw new DelegationError("unknownKey", grant.id, `no registered key for issuer ${issuer}`);
    }
    return this.keys.sign(issuer, grant);
  }
}
