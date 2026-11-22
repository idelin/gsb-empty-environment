import {
  canonicalJson,
  constantTimeEqual,
  fromBase64,
  fromHex,
  hmacSha256,
  toBase64,
  toHex,
  utf8,
} from "./canonical.ts";
import type {
  AliasKeyRing,
  AliasVerifier,
  SignedBrandAlias,
} from "./types.ts";
import {
  normalizeDomainKey,
  normalizeLabelKey,
  normalizeSuffix,
  splitDomainText,
} from "./unicode.ts";
import { suffixApplies } from "./suffix.ts";

export type AliasDraft = Omit<
  SignedBrandAlias,
  "signature" | "signatureEncoding" | "verified"
>;

export interface AliasContext {
  readonly brand: string;
  readonly suffix: string;
  readonly tableVersion: string;
  readonly suffixVersion: string;
  readonly now: number;
  readonly verifier?: AliasVerifier;
}

/**
 * The exact fields covered by an alias signature.  Keep this list stable; a
 * verifier in another process must reproduce this canonical JSON.
 */
export function aliasPayload(
  alias: SignedBrandAlias,
): Readonly<Record<string, unknown>> {
  return {
    alg: alias.alg ?? "hmac-sha256",
    brand: alias.brand,
    domain: alias.domain,
    keyId: alias.keyId,
    notAfter: alias.notAfter ?? null,
    notBefore: alias.notBefore ?? null,
    suffix: alias.suffix,
    suffixVersion: alias.suffixVersion ?? null,
    tableVersion: alias.tableVersion ?? null,
  };
}

export function aliasSigningText(alias: SignedBrandAlias): string {
  return canonicalJson(aliasPayload(alias));
}

function keyBytes(
  key: string | Uint8Array,
  encoding: "utf8" | "hex" | "base64" = "utf8",
): Uint8Array {
  if (key instanceof Uint8Array) return key;
  if (encoding === "hex") return fromHex(key);
  if (encoding === "base64") return fromBase64(key);
  return utf8(key);
}

export function signAlias(
  draft: AliasDraft,
  key: string | Uint8Array,
  options: {
    readonly keyEncoding?: "utf8" | "hex" | "base64";
    readonly signatureEncoding?: "hex" | "base64";
    readonly alg?: string;
  } = {},
): SignedBrandAlias {
  const alias: SignedBrandAlias = {
    ...draft,
    alg: options.alg ?? draft.alg ?? "hmac-sha256",
    signature: "",
    signatureEncoding: options.signatureEncoding ?? "hex",
  };
  const digest = hmacSha256(
    keyBytes(key, options.keyEncoding),
    utf8(aliasSigningText(alias)),
  );
  return {
    ...alias,
    signature:
      (options.signatureEncoding ?? "hex") === "base64"
        ? toBase64(digest)
        : toHex(digest),
  };
}

export function verifyAliasSignature(
  alias: SignedBrandAlias,
  key: string | Uint8Array,
  options: {
    readonly keyEncoding?: "utf8" | "hex" | "base64";
  } = {},
): boolean {
  if ((alias.alg ?? "hmac-sha256") !== "hmac-sha256") return false;
  let supplied: Uint8Array;
  try {
    supplied =
      (alias.signatureEncoding ?? "hex") === "base64"
        ? fromBase64(alias.signature)
        : fromHex(alias.signature);
  } catch {
    return false;
  }
  const expected = hmacSha256(
    keyBytes(key, options.keyEncoding),
    utf8(aliasSigningText(alias)),
  );
  return constantTimeEqual(expected, supplied);
}

export function createHmacAliasVerifier(
  keyring: AliasKeyRing,
  options: {
    readonly keyEncoding?: "utf8" | "hex" | "base64";
  } = {},
): AliasVerifier {
  return (alias) => {
    if (!Object.prototype.hasOwnProperty.call(keyring, alias.keyId)) {
      return false;
    }
    const key = keyring[alias.keyId];
    return verifyAliasSignature(alias, key, options);
  };
}

function domainHasSuffix(domain: string, suffix: string): boolean {
  const scope = normalizeSuffix(suffix);
  if (scope === "*") return true;
  const suffixLabels = splitDomainText(scope).map(normalizeLabelKey);
  if (suffixLabels.length === 0) return true;
  const domainLabels = splitDomainText(domain).map(normalizeLabelKey);
  if (suffixLabels.length > domainLabels.length) return false;
  const offset = domainLabels.length - suffixLabels.length;
  for (let i = 0; i < suffixLabels.length; i++) {
    if (domainLabels[offset + i] !== suffixLabels[i]) return false;
  }
  return true;
}

export function aliasApplies(
  alias: SignedBrandAlias,
  context: AliasContext,
): boolean {
  if (!Number.isFinite(context.now)) return false;
  if (alias.brand !== context.brand) return false;
  if (!suffixApplies(alias.suffix, context.suffix)) return false;
  if (alias.tableVersion !== undefined && alias.tableVersion !== context.tableVersion) {
    return false;
  }
  if (
    alias.suffixVersion !== undefined &&
    alias.suffixVersion !== context.suffixVersion
  ) {
    return false;
  }
  if (!domainHasSuffix(alias.domain, context.suffix)) return false;
  if (alias.notBefore !== undefined && context.now < alias.notBefore) return false;
  if (alias.notAfter !== undefined && context.now > alias.notAfter) return false;
  if (alias.verified === true) return true;
  return context.verifier ? context.verifier(alias) : false;
}

export interface ProvenAliases {
  /** Normalised domain keys. */
  readonly domains: ReadonlySet<string>;
  readonly records: readonly SignedBrandAlias[];
}

export function provenAliasDomains(
  aliases: readonly SignedBrandAlias[],
  context: AliasContext,
): ProvenAliases {
  const domains = new Set<string>();
  const records: SignedBrandAlias[] = [];
  for (const alias of aliases) {
    if (!aliasApplies(alias, context)) continue;
    const key = normalizeDomainKey(alias.domain);
    if (key.length === 0) continue;
    if (!domains.has(key)) {
      domains.add(key);
      records.push(alias);
    }
  }
  return { domains, records };
}
