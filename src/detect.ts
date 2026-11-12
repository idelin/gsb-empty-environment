import { parseSdp, effectiveAttribute, parseCandidate } from "./sdp.ts";
import type { Sdp } from "./sdp.ts";
import { parseNetwork, networkContains } from "./cidr.ts";
import type { Network } from "./cidr.ts";

export type ViolationCode =
  | "FINGERPRINT_CHANGED"
  | "ICE_UFRAG_CHANGED"
  | "ICE_PWD_CHANGED"
  | "MID_CHANGED"
  | "MEDIA_SECTION_COUNT_CHANGED"
  | "PAYLOAD_TYPES_CHANGED"
  | "UNREGISTERED_HOST_CANDIDATE";

export interface Violation {
  code: ViolationCode;
  message: string;
  mediaIndex?: number;
}

export interface DetectOptions {
  /** CIDR 网段列表，新增 host 候选必须落在其中之一，例如 ["10.0.0.0/8"] */
  registeredNetworks?: string[];
}

export interface DetectionResult {
  ok: boolean;
  violations: Violation[];
}

function sortedEqual(a: string[], b: string[]): boolean {
  if (a.length !== b.length) return false;
  const sa = [...a].sort();
  const sb = [...b].sort();
  return sa.every((value, index) => value === sb[index]);
}

function compareIntegrityAttributes(
  original: Sdp,
  received: Sdp,
  violations: Violation[],
): void {
  const checks: Array<{ name: string; code: ViolationCode }> = [
    { name: "fingerprint", code: "FINGERPRINT_CHANGED" },
    { name: "ice-ufrag", code: "ICE_UFRAG_CHANGED" },
    { name: "ice-pwd", code: "ICE_PWD_CHANGED" },
  ];

  for (const { name, code } of checks) {
    const sessionOriginal = original.sessionAttributes.get(name) ?? [];
    const sessionReceived = received.sessionAttributes.get(name) ?? [];
    if (!sortedEqual(sessionOriginal, sessionReceived)) {
      violations.push({
        code,
        message: `session-level a=${name} changed`,
      });
    }
    for (let i = 0; i < original.media.length; i++) {
      const before = effectiveAttribute(original, i, name);
      const after = effectiveAttribute(received, i, name);
      if (!sortedEqual(before, after)) {
        violations.push({
          code,
          mediaIndex: i,
          message: `a=${name} changed in media section ${i}`,
        });
      }
    }
  }

  for (let i = 0; i < original.media.length; i++) {
    const before = effectiveAttribute(original, i, "mid");
    const after = effectiveAttribute(received, i, "mid");
    if (!sortedEqual(before, after)) {
      violations.push({
        code: "MID_CHANGED",
        mediaIndex: i,
        message: `a=mid changed in media section ${i}`,
      });
    }
  }
}

function collectHostAddresses(sdp: Sdp): Set<string> {
  const addresses = new Set<string>();
  const sources: Array<Map<string, string[]>> = [
    sdp.sessionAttributes,
    ...sdp.media.map((section) => section.attributes),
  ];
  for (const attributes of sources) {
    for (const value of attributes.get("candidate") ?? []) {
      const candidate = parseCandidate(value);
      if (candidate && candidate.type === "host") {
        addresses.add(candidate.address);
      }
    }
  }
  return addresses;
}

export function detectSdpRewrite(
  originalSdp: string,
  receivedSdp: string,
  options: DetectOptions = {},
): DetectionResult {
  const violations: Violation[] = [];
  const original = parseSdp(originalSdp);
  const received = parseSdp(receivedSdp);

  if (original.media.length !== received.media.length) {
    violations.push({
      code: "MEDIA_SECTION_COUNT_CHANGED",
      message: `media section count changed from ${original.media.length} to ${received.media.length}`,
    });
  }

  const sharedSections = Math.min(original.media.length, received.media.length);
  for (let i = 0; i < sharedSections; i++) {
    const before = original.media[i];
    const after = received.media[i];
    if (
      before.media !== after.media ||
      before.proto !== after.proto ||
      !sortedEqual(before.formats, after.formats)
    ) {
      violations.push({
        code: "PAYLOAD_TYPES_CHANGED",
        mediaIndex: i,
        message: `m= line payload types changed in media section ${i} (only reordering is allowed)`,
      });
    }
  }

  compareIntegrityAttributes(original, received, violations);

  const networks: Network[] = [];
  for (const cidr of options.registeredNetworks ?? []) {
    const network = parseNetwork(cidr);
    if (network) networks.push(network);
  }

  const originalHostAddresses = collectHostAddresses(original);
  const sources: Array<{ attributes: Map<string, string[]>; mediaIndex?: number }> = [
    { attributes: received.sessionAttributes },
    ...received.media.map((section, mediaIndex) => ({
      attributes: section.attributes,
      mediaIndex,
    })),
  ];
  for (const source of sources) {
    for (const value of source.attributes.get("candidate") ?? []) {
      const candidate = parseCandidate(value);
      if (!candidate || candidate.type !== "host") continue;
      if (originalHostAddresses.has(candidate.address)) continue;
      const registered = networks.some((network) =>
        networkContains(network, candidate.address),
      );
      if (!registered) {
        violations.push({
          code: "UNREGISTERED_HOST_CANDIDATE",
          mediaIndex: source.mediaIndex,
          message: `new host candidate ${candidate.address} is not in registered networks`,
        });
      }
    }
  }

  return { ok: violations.length === 0, violations };
}
