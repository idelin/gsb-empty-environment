import {
  ALGORITHM_STRENGTH,
  canonicalAlgorithm,
  constantTimeEqualHex,
  digestHex,
  type HashAlgorithm,
} from "./hash.ts";
import {
  ErrorCode,
  PipelineError,
  type PipelineErrorDetails,
} from "./errors.ts";
import type {
  AssetManifest,
  DigestEntry,
  DigestGroup,
  RepresentationKind,
  RepresentationManifest,
} from "./manifest.ts";

export type GroupFailureReason =
  | "NO_SUPPORTED_ALGORITHM"
  | "DIGEST_MISMATCH"
  | "LENGTH_MISMATCH"
  | "EMPTY_GROUP";

export interface GroupDigestCheck {
  readonly algorithm: HashAlgorithm;
  readonly domain: string;
  readonly expected: string;
  readonly actual: string;
  readonly matched: boolean;
}

export interface GroupVerification {
  readonly group: string;
  readonly ok: boolean;
  readonly selectedAlgorithm: HashAlgorithm | null;
  readonly strongestSupported: HashAlgorithm | null;
  readonly reason?: GroupFailureReason;
  readonly checks: readonly GroupDigestCheck[];
}

export interface RepresentationVerification {
  readonly ok: boolean;
  readonly kind: RepresentationKind;
  readonly domain: string;
  readonly declaredLength: number;
  readonly actualLength: number;
  readonly groups: readonly GroupVerification[];
  readonly failures: readonly string[];
}

export interface VerifyOptions {
  readonly kind?: RepresentationKind;
  readonly expectedDomain?: string;
  readonly expectedLength?: number;
}

function strongestInGroup(entries: readonly DigestEntry[]): HashAlgorithm | null {
  let strongest: HashAlgorithm | null = null;
  for (const entry of entries) {
    const algorithm = canonicalAlgorithm(entry.algorithm);
    if (algorithm === null) {
      continue;
    }
    if (
      strongest === null ||
      ALGORITHM_STRENGTH.indexOf(algorithm) <
        ALGORITHM_STRENGTH.indexOf(strongest)
    ) {
      strongest = algorithm;
    }
  }
  return strongest;
}

/**
 * Verifies every group independently.  Inside a group only entries using the
 * strongest supported algorithm are considered.  Therefore a matching
 * SHA-256 value can never hide a mismatching SHA-512 value from the same
 * group, while an unsupported future algorithm does not block a supported
 * fallback.
 */
export function verifyDigestGroups(
  groups: readonly DigestGroup[],
  bytes: Uint8Array,
  context: {
    domain: string;
    declaredLength?: number;
    actualLength?: number;
  },
): GroupVerification[] {
  const actualLength = context.actualLength ?? bytes.byteLength;
  return groups.map((group, index) => {
    const groupName = group.id ?? `group-${index}`;
    if (
      group.length !== undefined &&
      group.length !== actualLength
    ) {
      return {
        group: groupName,
        ok: false,
        selectedAlgorithm: null,
        strongestSupported: strongestInGroup(group.entries),
        reason: "LENGTH_MISMATCH",
        checks: [],
      };
    }
    if (group.entries.length === 0) {
      return {
        group: groupName,
        ok: false,
        selectedAlgorithm: null,
        strongestSupported: null,
        reason: "EMPTY_GROUP",
        checks: [],
      };
    }

    const strongest = strongestInGroup(group.entries);
    if (strongest === null) {
      return {
        group: groupName,
        ok: false,
        selectedAlgorithm: null,
        strongestSupported: null,
        reason: "NO_SUPPORTED_ALGORITHM",
        checks: [],
      };
    }

    const selectedEntries = group.entries.filter(
      (entry) => canonicalAlgorithm(entry.algorithm) === strongest,
    );
    const digestCache = new Map<string, string>();
    const checks: GroupDigestCheck[] = selectedEntries.map((entry) => {
      const domain = entry.domain ?? group.domain ?? context.domain;
      const cacheKey = `${strongest}\u0000${domain}`;
      let actual = digestCache.get(cacheKey);
      if (actual === undefined) {
        actual = digestHex(strongest, domain, bytes);
        digestCache.set(cacheKey, actual);
      }
      return {
        algorithm: strongest,
        domain,
        expected: entry.digest,
        actual,
        matched: constantTimeEqualHex(entry.digest, actual),
      };
    });

    const matched = checks.some((check) => check.matched);
    return {
      group: groupName,
      ok: matched,
      selectedAlgorithm: strongest,
      strongestSupported: strongest,
      ...(matched ? {} : { reason: "DIGEST_MISMATCH" as const }),
      checks,
    };
  });
}

export function verifyRepresentation(
  representation: RepresentationManifest,
  bytes: Uint8Array,
  options: VerifyOptions = {},
): RepresentationVerification {
  const failures: string[] = [];
  if (options.kind !== undefined && representation.kind !== options.kind) {
    failures.push("representation-kind-mismatch");
  }
  if (
    options.expectedDomain !== undefined &&
    representation.domain !== options.expectedDomain
  ) {
    failures.push("domain-mismatch");
  }
  const expectedLength = options.expectedLength ?? representation.length;
  if (bytes.byteLength !== expectedLength) {
    failures.push("length-mismatch");
  }
  if (representation.length !== bytes.byteLength) {
    failures.push("declared-length-mismatch");
  }

  const groups =
    failures.length === 0
      ? verifyDigestGroups(representation.groups, bytes, {
          domain: representation.domain,
          declaredLength: expectedLength,
          actualLength: bytes.byteLength,
        })
      : representation.groups.map((group, index) => ({
          group: group.id ?? `group-${index}`,
          ok: false,
          selectedAlgorithm: null,
          strongestSupported: null,
          reason: "LENGTH_MISMATCH" as const,
          checks: [],
        }));

  for (const group of groups) {
    if (!group.ok) {
      failures.push(`group:${group.group}:${group.reason ?? "failed"}`);
    }
  }

  return {
    ok: failures.length === 0,
    kind: representation.kind,
    domain: representation.domain,
    declaredLength: representation.length,
    actualLength: bytes.byteLength,
    groups,
    failures,
  };
}

export function verificationDetails(
  verification: RepresentationVerification,
): PipelineErrorDetails {
  return {
    kind: verification.kind,
    domain: verification.domain,
    declaredLength: verification.declaredLength,
    actualLength: verification.actualLength,
    failures: verification.failures,
    groups: verification.groups.map((group) => ({
      group: group.group,
      ok: group.ok,
      selectedAlgorithm: group.selectedAlgorithm,
      reason: group.reason,
      checks: group.checks.map((check) => ({
        algorithm: check.algorithm,
        domain: check.domain,
        expected: check.expected,
        actual: check.actual,
        matched: check.matched,
      })),
    })),
  };
}

export function assertRepresentation(
  representation: RepresentationManifest,
  bytes: Uint8Array,
  options: VerifyOptions = {},
): RepresentationVerification {
  const verification = verifyRepresentation(representation, bytes, options);
  if (!verification.ok) {
    const code =
      verification.failures.includes("length-mismatch") ||
      verification.failures.includes("declared-length-mismatch")
        ? ErrorCode.LENGTH_MISMATCH
        : verification.failures.includes("domain-mismatch")
          ? ErrorCode.DOMAIN_MISMATCH
          : verification.failures.some((failure) =>
                failure.includes("NO_SUPPORTED_ALGORITHM"),
              )
            ? ErrorCode.UNSUPPORTED_ALGORITHM
          : ErrorCode.DIGEST_MISMATCH;
    throw new PipelineError(
      code,
      `failed to authenticate the ${representation.kind} representation`,
      verificationDetails(verification),
    );
  }
  return verification;
}

export interface AssetVerification {
  readonly ok: boolean;
  readonly transport: RepresentationVerification;
  readonly decoded: RepresentationVerification;
  readonly final?: RepresentationVerification;
  readonly decodedStage: "raw" | "final";
  readonly failures: readonly string[];
}

export function assertAssetRepresentations(
  manifest: AssetManifest,
  transportBytes: Uint8Array,
  decodedBytes: Uint8Array,
): AssetVerification {
  const transport = assertRepresentation(manifest.transport, transportBytes, {
    kind: "transport",
  });
  const decoded = assertRepresentation(manifest.decoded, decodedBytes, {
    kind: "decoded",
  });
  const final =
    manifest.final === undefined
      ? undefined
      : assertRepresentation(manifest.final, decodedBytes, { kind: "final" });
  return {
    ok: true,
    transport,
    decoded,
    ...(final === undefined ? {} : { final }),
    decodedStage: "raw",
    failures: [],
  };
}
