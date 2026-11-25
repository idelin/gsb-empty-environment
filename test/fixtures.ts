import {
  exportPublicKeyPem,
  generateKeyPair,
  importPublicKeyPem,
  sealStatement,
  type KeyPair,
} from "../src/signing.ts";
import { MapKeyDirectory } from "../src/keydir.ts";
import type {
  AliasAssertion,
  CorrectionStatement,
  GrantStatement,
  PurposeCatalog,
  RecoveryStatement,
  RegionCatalog,
  RevocationStatement,
  SignedAliasAssertion,
  SignedConsentStatement,
} from "../src/types.ts";

export const T0 = 1_000_000;
export const HOUR = 3_600_000;
export const DAY = 24 * HOUR;

export const purposes: PurposeCatalog = {
  parents: {
    root: null,
    marketing: "root",
    "marketing.newsletter": "marketing",
    "marketing.newsletter.weekly": "marketing.newsletter",
    "marketing.newsletter.daily": "marketing.newsletter",
    "marketing.promo": "marketing",
    transactional: "root",
    "transactional.receipt": "transactional",
  },
};

export const regions: RegionCatalog = {
  regions: new Set(["EU", "US", "APAC", "*"]),
  wildcard: "*",
};

export interface Issuer {
  key: KeyPair;
  chainId: string;
  seq: number;
}

export function makeIssuer(keyId: string, chainId: string): Issuer {
  return { key: generateKeyPair(keyId), chainId, seq: 0 };
}

function nextSeq(issuer: Issuer): number {
  issuer.seq += 1;
  return issuer.seq;
}

export interface GrantInput {
  id: string;
  subject: string;
  purpose: string;
  senders: string[];
  regions: string[];
  from: number;
  until: number;
  issuedAt?: number;
  seq?: number;
}

export function grant(
  issuer: Issuer,
  input: GrantInput,
): SignedConsentStatement {
  const statement: GrantStatement = {
    kind: "grant",
    id: input.id,
    chainId: issuer.chainId,
    issuer: issuer.key.keyId,
    seq: input.seq !== undefined ? input.seq : nextSeq(issuer),
    issuedAt: input.issuedAt ?? input.from - HOUR,
    subject: input.subject,
    purpose: input.purpose,
    senders: input.senders,
    regions: input.regions,
    validFrom: input.from,
    validUntil: input.until,
  };
  return sealStatement(statement, issuer.key);
}

export function recovery(
  issuer: Issuer,
  input: GrantInput & { supersedes: string },
): SignedConsentStatement {
  const statement: RecoveryStatement = {
    kind: "recovery",
    id: input.id,
    chainId: issuer.chainId,
    issuer: issuer.key.keyId,
    seq: nextSeq(issuer),
    issuedAt: input.issuedAt ?? input.from - HOUR,
    subject: input.subject,
    purpose: input.purpose,
    senders: input.senders,
    regions: input.regions,
    validFrom: input.from,
    validUntil: input.until,
    supersedes: input.supersedes,
  };
  return sealStatement(statement, issuer.key);
}

export interface RevocationInput {
  id: string;
  subject: string;
  targetGrantIds?: string[];
  filter: RevocationStatement["filter"];
  issuedAt: number;
}

export function revoke(
  issuer: Issuer,
  input: RevocationInput,
): SignedConsentStatement {
  const statement: RevocationStatement = {
    kind: "revocation",
    id: input.id,
    chainId: issuer.chainId,
    issuer: issuer.key.keyId,
    seq: nextSeq(issuer),
    issuedAt: input.issuedAt,
    subject: input.subject,
    targetGrantIds: input.targetGrantIds,
    filter: input.filter,
  };
  return sealStatement(statement, issuer.key);
}

export function correct(
  issuer: Issuer,
  id: string,
  issuedAt: number,
  replacement: SignedConsentStatement,
): SignedConsentStatement {
  const statement: CorrectionStatement = {
    kind: "correction",
    id,
    chainId: issuer.chainId,
    issuer: issuer.key.keyId,
    seq: nextSeq(issuer),
    issuedAt,
    correctsId: replacement.statement.id,
    replacement: replacement.statement as CorrectionStatement["replacement"],
  };
  return sealStatement(statement, issuer.key);
}

export function aliasAssertion(
  issuer: Issuer,
  id: string,
  alias: string,
  subject: string,
  issuedAt: number,
): SignedAliasAssertion {
  const statement: AliasAssertion = {
    id,
    issuer: issuer.key.keyId,
    issuedAt,
    alias,
    subject,
  };
  return sealStatement(statement, issuer.key);
}

export function keyDirectory(...issuers: Issuer[]): MapKeyDirectory {
  const directory = new MapKeyDirectory();
  for (const issuer of issuers) {
    directory.add(
      issuer.key.keyId,
      importPublicKeyPem(exportPublicKeyPem(issuer.key.publicKey)),
    );
  }
  return directory;
}

/** Deterministic Fisher-Yates shuffle driven by a seed (no RNG dependency). */
export function seededShuffle<T>(items: readonly T[], seed: number): T[] {
  const copy = [...items];
  let state = seed;
  for (let i = copy.length - 1; i > 0; i -= 1) {
    state = (state * 1103515245 + 12345) & 0x7fffffff;
    const j = state % (i + 1);
    [copy[i], copy[j]] = [copy[j] as T, copy[i] as T];
  }
  return copy;
}
