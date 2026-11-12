import type { DecodedDisclosure, Hasher } from "./disclosures.ts";
import { decodeDisclosures, sha256Hasher } from "./disclosures.ts";
import { SdJwtVerificationError } from "./errors.ts";

const SD_KEY = "_sd";
const ARRAY_MARKER = "...";
const REMOVED = Symbol("sd-jwt-removed-node");

interface DigestEntry {
  /** Container object holding the `_sd` array, or array holding the marker. */
  container: Record<string, unknown> | unknown[];
  /** Field name this digest was referenced from ("_sd" or an array index). */
  location: string;
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function assertReservedName(entry: DecodedDisclosure): void {
  if (entry.disclosure.kind !== "property") {
    return;
  }
  const { name } = entry.disclosure;
  if (name === SD_KEY || name === ARRAY_MARKER) {
    throw new SdJwtVerificationError(
      "RESERVED_DISCLOSURE_NAME",
      `Disclosure claim name "${name}" is reserved and must not be disclosed`,
    );
  }
}

/**
 * Validate every presented disclosure against the signed claims and return a
 * reconstructed copy of the claims. The input claims are never mutated.
 *
 * Checks performed:
 *  - digest of each disclosure appears exactly once in a signed `_sd` array
 *    or array `...` marker (nested containers included);
 *  - the same digest is never referenced twice in the signed claims;
 *  - no disclosure digest is presented twice;
 *  - every presented disclosure is actually referenced by the signed claims;
 *  - disclosed property names are neither `_sd` nor `...`;
 *  - disclosed property names do not collide with plaintext claim names
 *    inside the same object.
 */
export function verifyDisclosures(
  signedClaims: Record<string, unknown>,
  encodedDisclosures: readonly string[],
  hasher: Hasher = sha256Hasher,
): { claims: Record<string, unknown>; disclosures: DecodedDisclosure[] } {
  const claims = structuredClone(signedClaims);
  const decoded = decodeDisclosures(encodedDisclosures, hasher);

  // A holder presenting the same disclosure twice gets rejected outright.
  const seenPresentationDigests = new Set<string>();
  for (const entry of decoded) {
    assertReservedName(entry);
    if (seenPresentationDigests.has(entry.digest)) {
      throw new SdJwtVerificationError(
        "DUPLICATE_DISCLOSURE_DIGEST",
        `Disclosure digest ${entry.digest} is presented more than once`,
      );
    }
    seenPresentationDigests.add(entry.digest);
  }

  // Index every digest reference reachable from the signed claims. The same
  // digest referenced twice (including across nested objects) is rejected.
  const digestIndex = new Map<string, DigestEntry>();
  const indexedContainers = new WeakSet<object>();

  const registerDigest = (
    digest: unknown,
    container: Record<string, unknown> | unknown[],
    location: string,
  ): void => {
    if (typeof digest !== "string") {
      throw new SdJwtVerificationError(
        "MALFORMED_SD_ARRAY",
        "Every _sd entry / array marker hash must be a base64url string",
      );
    }
    if (digestIndex.has(digest)) {
      throw new SdJwtVerificationError(
        "DUPLICATE_SD_DIGEST",
        `Digest ${digest} is referenced more than once in the signed claims`,
      );
    }
    digestIndex.set(digest, { container, location });
  };

  const indexNode = (node: unknown): void => {
    if (Array.isArray(node)) {
      if (indexedContainers.has(node)) {
        return;
      }
      indexedContainers.add(node);
      node.forEach((item, arrayIndex) => {
        if (isPlainObject(item) && ARRAY_MARKER in item) {
          if (
            Object.keys(item).length !== 1 ||
            typeof item[ARRAY_MARKER] !== "string"
          ) {
            throw new SdJwtVerificationError(
              "MALFORMED_ARRAY_MARKER",
              "An array disclosure marker must be an object with only a string \"...\" key",
            );
          }
          registerDigest(item[ARRAY_MARKER], node, String(arrayIndex));
        } else {
          indexNode(item);
        }
      });
      return;
    }

    if (!isPlainObject(node) || indexedContainers.has(node)) {
      return;
    }
    indexedContainers.add(node);

    if (SD_KEY in node) {
      const sdArray = node[SD_KEY];
      if (!Array.isArray(sdArray)) {
        throw new SdJwtVerificationError(
          "MALFORMED_SD_ARRAY",
          "The _sd claim must be an array of digest strings",
        );
      }
      for (const digest of sdArray) {
        registerDigest(digest, node, SD_KEY);
      }
    }

    for (const [key, value] of Object.entries(node)) {
      if (key !== SD_KEY) {
        indexNode(value);
      }
    }
  };
  indexNode(claims);

  // Apply disclosures to fixpoint: a disclosed value may itself carry _sd
  // arrays or array markers, so newly inserted subtrees are indexed and may
  // unlock further disclosures regardless of presentation order.
  const appliedDigests = new Set<string>();
  let madeProgress = true;
  while (madeProgress) {
    madeProgress = false;
    for (const entry of decoded) {
      if (appliedDigests.has(entry.digest)) {
        continue;
      }
      const reference = digestIndex.get(entry.digest);
      if (!reference) {
        continue;
      }

      if (entry.disclosure.kind === "property") {
        const container = reference.container as Record<string, unknown>;
        if (
          Object.prototype.hasOwnProperty.call(
            container,
            entry.disclosure.name,
          )
        ) {
          throw new SdJwtVerificationError(
            "CLAIM_NAME_CONFLICT",
            `Disclosed claim name "${entry.disclosure.name}" conflicts with a plaintext claim`,
          );
        }
        container[entry.disclosure.name] = entry.disclosure.value;
      } else {
        const array = reference.container as unknown[];
        array[Number(reference.location)] = entry.disclosure.value;
      }

      appliedDigests.add(entry.digest);
      indexNode(entry.disclosure.value);
      madeProgress = true;
    }
  }

  const unresolved = decoded.find(
    (entry) => !appliedDigests.has(entry.digest),
  );
  if (unresolved) {
    throw new SdJwtVerificationError(
      "UNREFERENCED_DISCLOSURE",
      `Disclosure digest ${unresolved.digest} does not match any digest signed by the issuer`,
    );
  }

  // Strip unpresented _sd arrays and unresolved array markers per RFC 9812.
  const finalize = (node: unknown): unknown => {
    if (Array.isArray(node)) {
      const kept: unknown[] = [];
      for (const item of node) {
        if (
          isPlainObject(item) &&
          Object.keys(item).length === 1 &&
          ARRAY_MARKER in item
        ) {
          continue;
        }
        const child = finalize(item);
        if (child !== REMOVED) {
          kept.push(child);
        }
      }
      return kept;
    }
    if (!isPlainObject(node)) {
      return node;
    }

    const containedSd = SD_KEY in node;
    const output: Record<string, unknown> = {};
    for (const [key, value] of Object.entries(node)) {
      if (key === SD_KEY) {
        continue;
      }
      const child = finalize(value);
      if (child !== REMOVED) {
        output[key] = child;
      }
    }
    if (containedSd && Object.keys(output).length === 0) {
      return REMOVED;
    }
    return output;
  };

  const finalized = finalize(claims);
  return {
    claims: (finalized === REMOVED ? {} : finalized) as Record<
      string,
      unknown
    >,
    disclosures: decoded,
  };
}
