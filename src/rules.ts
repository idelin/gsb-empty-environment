/**
 * 受签 trustedRulesRoot 与公共后缀规则证明束。
 *
 * 决策引擎只持有带外交付的信任锚（Ed25519 公钥）与证明束，
 * 不持有完整规则列表；规则的存在/缺失全部经 Merkle 证明独立核验。
 */

import type { HashFn } from './merkle.ts';
import {
  buildMerkleTree,
  fromHex,
  proveExclusion,
  proveInclusion,
  rootHex,
  verifyMerkleProof,
  type MerkleProof,
  type MerkleTree,
} from './merkle.ts';
import { buildRuleMap, candidateKeys, normalizeHost, resolvePsl, type PslResolution } from './psl.ts';
import { signDetached, verifyDetached } from './crypto.ts';

/** 带外信任锚：缺失即拒绝，任何决策都不允许回退到内置规则。 */
export interface TrustAnchor {
  /** 信任锚标识。 */
  readonly anchorId: string;
  /** Ed25519 SPKI DER 公钥。 */
  readonly ed25519PublicKey: Uint8Array;
}

export interface SignedRulesRoot {
  /** 签名覆盖的规范载荷。 */
  readonly payload: {
    readonly anchorId: string;
    readonly policyVersion: string;
    readonly merkleRootHex: string;
    readonly leafCount: number;
    readonly issuedAtMs: number;
  };
  /** 对 canonicalPayload(payload) 的 Ed25519 分离签名。 */
  readonly signature: Uint8Array;
}

export interface HostRulesProof {
  readonly host: string;
  /** 候选键 -> 包含或缺失证明，键集合必须覆盖 candidateKeys(host)。 */
  readonly proofs: Readonly<Record<string, MerkleProof>>;
}

export interface RulesProofBundle {
  readonly signedRoot: SignedRulesRoot;
  /** 每个请求主机（含重定向各跳）一份证明。 */
  readonly hostProofs: ReadonlyArray<HostRulesProof>;
}

export type RootVerification =
  | { readonly ok: true; readonly merkleRoot: Uint8Array; readonly policyVersion: string }
  | { readonly ok: false; readonly reason: 'missing-external-root' | 'untrusted-rules-root' };

export function canonicalPayload(payload: SignedRulesRoot['payload']): Uint8Array {
  const text = [
    'trusted-rules-root/v1',
    payload.anchorId,
    payload.policyVersion,
    payload.merkleRootHex,
    String(payload.leafCount),
    String(payload.issuedAtMs),
  ].join('\n');
  return new TextEncoder().encode(text);
}

/** 规则持有方：构建树并签发根。 */
export class RulesProver {
  private readonly ruleMap: Map<string, string>;
  private readonly tree: MerkleTree;
  private readonly hash: HashFn;

  constructor(rules: readonly string[], hash: HashFn) {
    this.hash = hash;
    const classified = buildRuleMap(rules);
    this.ruleMap = new Map(Array.from(classified.keys(), (key) => [key, '1']));
    this.tree = buildMerkleTree(Array.from(this.ruleMap.entries()), hash);
  }

  get merkleRootHex(): string {
    return rootHex(this.tree);
  }

  get leafCount(): number {
    return this.tree.keys.length;
  }

  signRoot(anchorId: string, policyVersion: string, issuedAtMs: number, privateKeyPkcs8Pem: string): SignedRulesRoot {
    const payload = {
      anchorId,
      policyVersion,
      merkleRootHex: this.merkleRootHex,
      leafCount: this.leafCount,
      issuedAtMs,
    };
    const signature = signDetached(privateKeyPkcs8Pem, canonicalPayload(payload));
    return { payload, signature };
  }

  proveHost(host: string): HostRulesProof {
    const normalized = normalizeHost(host);
    const proofs: Record<string, MerkleProof> = {};
    for (const key of candidateKeys(normalized)) {
      proofs[key] = this.tree.keys.includes(key)
        ? proveInclusion(this.tree, this.ruleMap, key, this.hash)
        : proveExclusion(this.tree, this.ruleMap, key, this.hash);
    }
    return { host: normalized, proofs };
  }

  proveBundle(hosts: readonly string[], signedRoot: SignedRulesRoot): RulesProofBundle {
    const seen = new Set<string>();
    const hostProofs: HostRulesProof[] = [];
    for (const host of hosts) {
      const normalized = normalizeHost(host);
      if (seen.has(normalized)) {
        continue;
      }
      seen.add(normalized);
      hostProofs.push(this.proveHost(normalized));
    }
    return { signedRoot, hostProofs };
  }
}

/**
 * 独立核验组件：只依赖信任锚与证明束，可被宿主单独调用。
 */
export class RulesVerifier {
  constructor(
    private readonly anchor: TrustAnchor | null,
    private readonly hash: HashFn,
  ) {}

  verifyRoot(bundle: RulesProofBundle | null): RootVerification {
    // 强制带外根：外部根或证明束缺失，直接拒绝。
    if (this.anchor === null || bundle === null) {
      return { ok: false, reason: 'missing-external-root' };
    }
    const { payload, signature } = bundle.signedRoot;
    if (payload.anchorId !== this.anchor.anchorId) {
      return { ok: false, reason: 'untrusted-rules-root' };
    }
    const signatureValid = verifyDetached(
      this.anchor.ed25519PublicKey,
      canonicalPayload(payload),
      signature,
    );
    if (!signatureValid) {
      return { ok: false, reason: 'untrusted-rules-root' };
    }
    let merkleRoot: Uint8Array;
    try {
      merkleRoot = fromHex(payload.merkleRootHex);
    } catch {
      return { ok: false, reason: 'untrusted-rules-root' };
    }
    if (payload.leafCount < 0) {
      return { ok: false, reason: 'untrusted-rules-root' };
    }
    const leafCountConsistent = bundle.hostProofs.every((hostProof) =>
      Object.values(hostProof.proofs).every((proof) =>
        proof.kind === 'inclusion' ? true : proof.leafCount === payload.leafCount,
      ),
    );
    if (!leafCountConsistent) {
      return { ok: false, reason: 'untrusted-rules-root' };
    }
    return { ok: true, merkleRoot, policyVersion: payload.policyVersion };
  }

  /** 核验单主机证明并返回 PSL 求值；任何证明失败都抛出，由调用方转拒绝。 */
  verifyHostProof(bundle: RulesProofBundle, merkleRoot: Uint8Array, host: string): PslResolution {
    const normalized = normalizeHost(host);
    const hostProof = bundle.hostProofs.find((entry) => entry.host === normalized);
    if (!hostProof) {
      throw new Error(`missing host proof for ${normalized}`);
    }
    const required = new Set(candidateKeys(normalized));
    const present = new Set<string>();
    for (const [key, proof] of Object.entries(hostProof.proofs)) {
      if (!required.has(key)) {
        throw new Error(`unexpected proof key ${key} for ${normalized}`);
      }
      if (!verifyMerkleProof(proof, merkleRoot, this.hash)) {
        throw new Error(`invalid merkle proof for ${key} on ${normalized}`);
      }
      if (proof.kind === 'inclusion') {
        if (proof.key !== key) {
          throw new Error(`inclusion proof key mismatch for ${key}`);
        }
        present.add(key);
      } else {
        if (proof.key !== key) {
          throw new Error(`exclusion proof key mismatch for ${key}`);
        }
      }
    }
    for (const key of required) {
      if (!(key in hostProof.proofs)) {
        throw new Error(`incomplete proof set: missing ${key} for ${normalized}`);
      }
    }
    return resolvePsl(normalized, present);
  }
}
