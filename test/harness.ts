import {
  BFCacheStore,
  generateEd25519TestKey,
  RulesProver,
  sha256,
  type AncestorEntry,
  type CookieInput,
  type FrameNode,
  type Origin,
  type RequestInput,
  type RulesProofBundle,
  type SiteKey,
  type TrustAnchor,
} from '../src/index.ts';

export const PSL_RULES = [
  'com',
  'org',
  'co.uk',
  '*.ck',
  '!www.ck',
  'github.io',
];

export interface Harness {
  readonly anchor: TrustAnchor;
  readonly prover: RulesProver;
  readonly privateKey: string;
  readonly policyVersion: string;
}

export function buildHarness(policyVersion = 'policy-2026-11'): Harness {
  const key = generateEd25519TestKey();
  const anchor: TrustAnchor = { anchorId: 'anchor-test-1', ed25519PublicKey: key.publicKeySpkiDer };
  const prover = new RulesProver(PSL_RULES, sha256);
  return { anchor, prover, privateKey: key.privateKeyPkcs8Pem, policyVersion };
}

export function signedBundle(
  harness: Harness,
  hosts: readonly string[],
  issuedAtMs: number,
  version = harness.policyVersion,
): RulesProofBundle {
  const signedRoot = harness.prover.signRoot(harness.anchor.anchorId, version, issuedAtMs, harness.privateKey);
  return harness.prover.proveBundle(hosts, signedRoot);
}

export function httpOrigin(host: string, scheme = 'https'): Origin {
  return { opaque: false, scheme, host, port: null };
}

export function opaqueOrigin(tag: string): Origin {
  return { opaque: true, identityTag: tag };
}

export function ancestorChain(...origins: Array<Origin | { origin: Origin; sandboxed?: boolean; allowSameOrigin?: boolean }>): AncestorEntry[] {
  return origins.map((entry, index) => {
    if ('opaque' in entry) {
      return {
        frameId: `frame-${index}`,
        effectiveOrigin: entry,
        sandboxed: entry.opaque,
        allowSameOrigin: false,
      };
    }
    return {
      frameId: `frame-${index}`,
      effectiveOrigin: entry.origin,
      sandboxed: entry.sandboxed ?? false,
      allowSameOrigin: entry.allowSameOrigin ?? false,
    };
  });
}

export function frameTree(...chain: AncestorEntry[]): FrameNode {
  let root: FrameNode | null = null;
  let cursor: FrameNode | null = null;
  for (const entry of chain) {
    const node: FrameNode = {
      id: entry.frameId,
      effectiveOrigin: entry.effectiveOrigin,
      sandboxed: entry.sandboxed,
      allowSameOrigin: entry.allowSameOrigin,
      children: [],
    };
    if (!root) {
      root = node;
      cursor = node;
    } else {
      (cursor!.children as FrameNode[]).push(node);
      cursor = node;
    }
  }
  return root!;
}

export function cookie(partial: Partial<CookieInput> & Pick<CookieInput, 'name' | 'domain'>): CookieInput {
  return {
    path: '/',
    sameSite: 'lax',
    secure: true,
    partitioned: false,
    createdAtMs: 0,
    ...partial,
  };
}

export function site(scheme: string, registrableDomain: string): SiteKey {
  return { scheme, registrableDomain };
}

export function request(partial: Partial<RequestInput> & Pick<RequestInput, 'url' | 'method'>): RequestInput {
  return {
    initiatedAtMs: 1_000_000,
    ancestorChain: ancestorChain(httpOrigin(new URL(partial.url).hostname, new URL(partial.url).protocol.slice(0, -1))),
    topLevelNavigation: true,
    hasUserActivation: true,
    redirects: [],
    policyVersion: 'policy-2026-11',
    ...partial,
  };
}

export { BFCacheStore };
