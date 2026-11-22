import { createHash, randomBytes } from 'node:crypto';
import type { GateExpr } from './bdd.ts';

export interface SourceExpr {
  scheme: 'https' | 'wss';
  host: string;
  pathPrefix?: string;
}

export interface ScriptSpec {
  id: string;
  /** Raw script bytes. The content commitment is their SHA-256 digest. */
  bytes: Uint8Array;
  /** Inheritable child-script sources declared at this fragment node. */
  inheritableSources?: SourceExpr[];
}

export interface LoadSpec {
  /** Script in the current fragment node that initiates the load. */
  fromScriptId: string;
  /** Script within the fragment filling `slot` that the current script loads. */
  slot: string;
  targetScriptId: string;
  source: SourceExpr;
  /** Symbolic switch gate for this loading rule. */
  gate: GateExpr;
}

export interface FragmentNode {
  id: string;
  scripts: ScriptSpec[];
  /** Outgoing loading rules; their gates form edges of a possibly cyclic graph. */
  loads: LoadSpec[];
  /** Child fragments keyed by slot name. */
  slots?: Record<string, SlotRef>;
}

export interface SlotRef {
  fragment: FragmentNode;
  /** Position-specific override of the inheritable child-script sources. */
  inheritSources?: SourceExpr[];
}

export type TrustedRoot =
  | {
      mode: 'nonce';
      scriptId: string;
      /** Symbolic switches under which this root trust is active. */
      gate: GateExpr;
    }
  | {
      mode: 'hash';
      scriptId: string;
      commitment: string;
      gate: GateExpr;
    }
  | {
      mode: 'host';
      source: SourceExpr;
      gate: GateExpr;
    };

export interface CspPolicy {
  id: string;
  strictDynamic: boolean;
  trustedRoots: TrustedRoot[];
}

export function sha256(bytes: Uint8Array): string {
  return createHash('sha256').update(bytes).digest('hex');
}

export function sourceMatches(source: SourceExpr, candidate: SourceExpr): boolean {
  if (source.scheme !== candidate.scheme || source.host !== candidate.host) {
    return false;
  }
  if (source.pathPrefix === undefined) {
    return true;
  }
  return candidate.pathPrefix !== undefined && candidate.pathPrefix.startsWith(source.pathPrefix);
}

export interface IssuedNonce {
  value: string;
  responseId: string;
  positionKey: string;
  scriptId: string;
  commitment: string;
  published: boolean;
}

/**
 * Per-response nonce authority. A fresh authority is created for every
 * response, so a nonce minted for an older response can never be accepted on
 * a new page.
 */
export class NonceAuthority {
  private readonly issued = new Map<string, IssuedNonce>();
  readonly responseId: string;

  constructor(responseId: string) {
    this.responseId = responseId;
  }

  mint(input: { positionKey: string; scriptId: string; commitment: string }): string {
    const value = randomBytes(16).toString('base64url');
    this.issued.set(value, {
      value,
      responseId: this.responseId,
      positionKey: input.positionKey,
      scriptId: input.scriptId,
      commitment: input.commitment,
      published: false,
    });
    return value;
  }

  publish(value: string): void {
    const record = this.issued.get(value);
    if (record) {
      record.published = true;
    }
  }

  /**
   * A nonce is honored only when it belongs to this response, is bound to the
   * exact positioned script and commitment, and has actually been published.
   */
  verify(
    value: string | undefined,
    expected: { responseId: string; positionKey: string; scriptId: string; commitment: string },
  ): { ok: true } | { ok: false; reason: NonceFailure } {
    if (value === undefined) {
      return { ok: false, reason: 'nonce-not-published' };
    }
    const record = this.issued.get(value);
    if (!record) {
      return { ok: false, reason: 'nonce-foreign-response' };
    }
    if (record.responseId !== expected.responseId || this.responseId !== expected.responseId) {
      return { ok: false, reason: 'nonce-foreign-response' };
    }
    if (!record.published) {
      return { ok: false, reason: 'nonce-not-published' };
    }
    if (
      record.positionKey !== expected.positionKey ||
      record.scriptId !== expected.scriptId ||
      record.commitment !== expected.commitment
    ) {
      return { ok: false, reason: 'nonce-binding-mismatch' };
    }
    return { ok: true };
  }

}

export type NonceFailure =
  | 'nonce-foreign-response'
  | 'nonce-not-published'
  | 'nonce-binding-mismatch';

export type DenialCode =
  | NonceFailure
  | 'source-not-inheritable'
  | 'commitment-mismatch'
  | 'host-only-under-strict-dynamic'
  | 'orphan-cycle-no-root';
