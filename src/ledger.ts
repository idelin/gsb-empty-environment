import { randomUUID } from 'node:crypto';
import { canonicalize, KeyRegistry } from './crypto.ts';
import { evaluate, validateCondition } from './policy.ts';
import type {
  Attributes,
  ChainProof,
  Condition,
  Delegation,
  LinkCheck,
  PolicyTraceEntry,
  ProofLink,
} from './types.ts';

export class DelegationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'DelegationError';
  }
}

export interface IssueParams {
  readonly subject: string;
  readonly resources: readonly string[];
  readonly actions: readonly string[];
  readonly policy?: Condition | null;
  readonly maxDepth: number;
  readonly delegatable: boolean;
  readonly notBefore: number;
  readonly expiresAt: number;
}

export interface DelegateParams extends IssueParams {
  readonly parentId: string;
  /** Issuance time; the parent must be valid at this moment. */
  readonly now: number;
}

export interface AuthorizeParams {
  readonly tokenId: string;
  readonly resource: string;
  readonly action: string;
  /** Explicit attributes; the only thing policies are allowed to read. */
  readonly attributes?: Attributes;
  readonly now: number;
}

export interface AuthorizeResult {
  readonly allowed: boolean;
  readonly reason: string;
  readonly proof: ChainProof;
}

type UnsignedDelegation = Omit<Delegation, 'signature'>;

/**
 * Issues, stores, revokes and verifies delegation tokens. All state is
 * in-memory; verification always walks the full ancestor chain so that
 * revoking or expiring an ancestor invalidates every descendant.
 */
export class DelegationLedger {
  readonly #keys: KeyRegistry;
  readonly #rootPrincipal: string;
  readonly #tokens = new Map<string, Delegation>();
  readonly #revoked = new Set<string>();

  constructor(keys: KeyRegistry, rootPrincipal = 'root') {
    if (!keys.has(rootPrincipal)) {
      throw new DelegationError(`root principal "${rootPrincipal}" has no registered key`);
    }
    this.#keys = keys;
    this.#rootPrincipal = rootPrincipal;
  }

  /** Root token: signed by the root authority, no parent. */
  issueRoot(params: IssueParams): Delegation {
    return this.#mint(this.#rootPrincipal, null, params);
  }

  /**
   * Child delegation. Enforces every attenuation rule at issuance time:
   * scope subset, depth budget, delegatable flag, time-window containment,
   * parent currently valid, and no delegation cycles.
   */
  delegate(params: DelegateParams): Delegation {
    const parent = this.#requireToken(params.parentId);
    const ancestors = this.getChain(parent.id);

    if (!parent.delegatable) {
      throw new DelegationError(`token ${parent.id} is marked non-delegatable`);
    }
    if (parent.maxDepth < 1) {
      throw new DelegationError(`token ${parent.id} has no remaining delegation depth`);
    }
    if (params.maxDepth > parent.maxDepth - 1) {
      throw new DelegationError(
        `maxDepth ${params.maxDepth} exceeds parent's remaining budget ${parent.maxDepth - 1}`,
      );
    }
    if (this.#isRevokedDeep(parent.id)) {
      throw new DelegationError(`token ${parent.id} or one of its ancestors is revoked`);
    }
    if (!(parent.notBefore <= params.now && params.now < parent.expiresAt)) {
      throw new DelegationError(`parent token ${parent.id} is not valid at issuance time ${params.now}`);
    }
    if (params.notBefore < parent.notBefore || params.expiresAt > parent.expiresAt) {
      throw new DelegationError('child validity window must fit inside the parent window');
    }
    const extraResources = params.resources.filter((r) => !parent.resources.includes(r));
    if (extraResources.length > 0) {
      throw new DelegationError(`resource scope expansion rejected: ${extraResources.join(', ')}`);
    }
    const extraActions = params.actions.filter((a) => !parent.actions.includes(a));
    if (extraActions.length > 0) {
      throw new DelegationError(`action scope expansion rejected: ${extraActions.join(', ')}`);
    }
    const chainPrincipals = new Set(ancestors.flatMap((t) => [t.issuer, t.subject]));
    if (chainPrincipals.has(params.subject)) {
      throw new DelegationError(
        `delegation cycle rejected: "${params.subject}" already appears in the ancestor chain`,
      );
    }

    return this.#mint(parent.subject, parent.id, params);
  }

  /** Revoke a token; every descendant becomes invalid through chain verification. */
  revoke(tokenId: string): void {
    this.#requireToken(tokenId);
    this.#revoked.add(tokenId);
  }

  isRevoked(tokenId: string): boolean {
    return this.#revoked.has(tokenId);
  }

  /** Chain from root to the given token, inclusive. */
  getChain(tokenId: string): Delegation[] {
    const chain: Delegation[] = [];
    const seen = new Set<string>();
    let cursor: Delegation | undefined = this.#requireToken(tokenId);
    while (cursor) {
      if (seen.has(cursor.id)) {
        throw new DelegationError(`corrupt ledger: parent loop at token ${cursor.id}`);
      }
      seen.add(cursor.id);
      chain.unshift(cursor);
      cursor = cursor.parentId === null ? undefined : this.#tokens.get(cursor.parentId);
      if (cursor === undefined && chain[0].parentId !== null) {
        throw new DelegationError(`corrupt ledger: missing parent token ${chain[0].parentId}`);
      }
    }
    return chain;
  }

  /**
   * Verify the full chain for a token at time `now` and produce a proof.
   * A token is valid only if EVERY link — itself included — is signed,
   * unrevoked, within its validity window, and structurally attenuating.
   */
  verifyChain(tokenId: string, now: number): ChainProof {
    const policyTrace: PolicyTraceEntry[] = [];
    const base = { tokenId, verifiedAt: now, policyTrace };
    let chain: Delegation[];
    try {
      chain = this.getChain(tokenId);
    } catch (error) {
      return { ...base, valid: false, failure: (error as Error).message, chain: [] };
    }

    const links: ProofLink[] = [];
    // The holder sequence root -> s1 -> s2 -> ... must be duplicate-free.
    // A non-root issuer is always the previous subject (checked above as
    // chain-linkage), so only each new subject can close a cycle.
    const principalsSeen = new Set<string>([this.#rootPrincipal]);
    let firstFailure: string | null = null;

    for (let index = 0; index < chain.length; index += 1) {
      const token = chain[index];
      const parent = index === 0 ? null : chain[index - 1];
      const checks: LinkCheck[] = [];
      const check = (name: string, ok: boolean, detail: string): void => {
        checks.push({ name, ok, detail });
        if (!ok && firstFailure === null) {
          firstFailure = `token ${token.id}: ${name} — ${detail}`;
        }
      };

      const payload = canonicalize(stripSignature(token));
      check(
        'signature',
        this.#keys.verify(token.issuer, payload, token.signature),
        `signed by "${token.issuer}"`,
      );
      check('not-revoked', !this.#revoked.has(token.id), this.#revoked.has(token.id) ? 'revoked' : 'active');
      check(
        'within-validity-window',
        token.notBefore <= now && now < token.expiresAt,
        `window [${token.notBefore}, ${token.expiresAt}), now=${now}`,
      );

      if (parent === null) {
        check(
          'root-structure',
          token.issuer === this.#rootPrincipal && token.parentId === null,
          `issuer must be root authority "${this.#rootPrincipal}" with no parent`,
        );
      } else {
        check(
          'chain-linkage',
          parent.subject === token.issuer && token.parentId === parent.id,
          `issued by holder "${parent.subject}" of parent token`,
        );
        check('parent-delegatable', parent.delegatable, parent.delegatable ? 'delegatable' : 'non-delegatable');
        check(
          'depth-budget',
          parent.maxDepth >= 1 && token.maxDepth <= parent.maxDepth - 1,
          `depth ${token.maxDepth} within parent budget ${parent.maxDepth}`,
        );
        check(
          'resource-attenuation',
          token.resources.every((r) => parent.resources.includes(r)),
          `resources ⊆ parent resources`,
        );
        check(
          'action-attenuation',
          token.actions.every((a) => parent.actions.includes(a)),
          `actions ⊆ parent actions`,
        );
        check(
          'window-attenuation',
          token.notBefore >= parent.notBefore && token.expiresAt <= parent.expiresAt,
          'validity window ⊆ parent window',
        );
      }

      const cycleHit = principalsSeen.has(token.subject) ? token.subject : undefined;
      check(
        'no-cycle',
        cycleHit === undefined,
        cycleHit === undefined ? 'no repeated principal' : `principal "${cycleHit}" repeats in chain`,
      );
      principalsSeen.add(token.subject);

      links.push({
        tokenId: token.id,
        issuer: token.issuer,
        subject: token.subject,
        resources: token.resources,
        actions: token.actions,
        notBefore: token.notBefore,
        expiresAt: token.expiresAt,
        checks,
      });
    }

    return { ...base, valid: firstFailure === null, failure: firstFailure, chain: links };
  }

  /**
   * Authorize one concrete access. Requires a valid chain, the resource and
   * action in the leaf scope, and every policy along the chain to accept the
   * explicit attributes. Returns the full chain proof either way.
   */
  authorize(params: AuthorizeParams): AuthorizeResult {
    const proof = this.verifyChain(params.tokenId, params.now);
    const fail = (reason: string): AuthorizeResult => ({ allowed: false, reason, proof });

    if (!proof.valid) {
      return fail(proof.failure ?? 'chain invalid');
    }
    const leaf = this.#requireToken(params.tokenId);
    if (!leaf.resources.includes(params.resource)) {
      return fail(`resource "${params.resource}" not in token scope`);
    }
    if (!leaf.actions.includes(params.action)) {
      return fail(`action "${params.action}" not in token scope`);
    }

    const attributes: Attributes = params.attributes ?? {};
    const policyTrace: PolicyTraceEntry[] = [];
    for (const token of this.getChain(params.tokenId)) {
      if (token.policy === null) {
        continue;
      }
      const outcome = evaluate(token.policy, attributes);
      policyTrace.push({ tokenId: token.id, ok: outcome.ok, attributesRead: outcome.attributesRead });
      if (!outcome.ok) {
        return {
          allowed: false,
          reason: `policy on token ${token.id} rejected the attributes`,
          proof: { ...proof, policyTrace },
        };
      }
    }
    return { allowed: true, reason: 'ok', proof: { ...proof, policyTrace } };
  }

  #mint(issuer: string, parentId: string | null, params: IssueParams): Delegation {
    if (params.policy !== null && params.policy !== undefined) {
      validateCondition(params.policy);
    }
    if (!Number.isInteger(params.maxDepth) || params.maxDepth < 0) {
      throw new DelegationError('maxDepth must be a non-negative integer');
    }
    if (!(params.notBefore < params.expiresAt)) {
      throw new DelegationError('notBefore must be strictly before expiresAt');
    }
    if (params.resources.length === 0 || params.actions.length === 0) {
      throw new DelegationError('resources and actions must be non-empty finite sets');
    }
    const unsigned: UnsignedDelegation = {
      id: randomUUID(),
      issuer,
      subject: params.subject,
      resources: [...new Set(params.resources)].sort(),
      actions: [...new Set(params.actions)].sort(),
      policy: params.policy ?? null,
      maxDepth: params.maxDepth,
      delegatable: params.delegatable,
      notBefore: params.notBefore,
      expiresAt: params.expiresAt,
      parentId,
    };
    const token: Delegation = {
      ...unsigned,
      signature: this.#keys.sign(issuer, canonicalize(unsigned)),
    };
    this.#tokens.set(token.id, token);
    return token;
  }

  #requireToken(tokenId: string): Delegation {
    const token = this.#tokens.get(tokenId);
    if (!token) {
      throw new DelegationError(`unknown token ${tokenId}`);
    }
    return token;
  }

  #isRevokedDeep(tokenId: string): boolean {
    return this.getChain(tokenId).some((token) => this.#revoked.has(token.id));
  }
}

function stripSignature(token: Delegation): UnsignedDelegation {
  const { signature: _signature, ...unsigned } = token;
  return unsigned;
}
