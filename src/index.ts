import type {
  ParsedMpd,
  ValidationOptions,
  ValidationResult,
} from './types.ts';
import { parseMpd } from './mpd.ts';
import { validateManifest } from './validator.ts';

/**
 * 端到端校验入口：传入 MPD 文本与“Representation id -> 初始化分段”映射。
 *
 * 典型用法：
 * ```ts
 * const result = await validateDashManifest(mpdText, {
 *   fetchInitSegment: async (id) => new Uint8Array(await (await fetch(url)).arrayBuffer()),
 *   allowedLicenseUrls: ['https://license.example.com/'],
 * });
 * if (!result.ok) throw result.issues;
 * ```
 */
export async function validateDashManifest(
  mpdXml: string,
  config: ValidationOptions & {
    fetchInitSegment: (representationId: string) => Promise<Uint8Array | ArrayBuffer> | Uint8Array | ArrayBuffer;
  },
): Promise<ValidationResult> {
  const { fetchInitSegment, ...options } = config;
  const manifest = parseMpd(mpdXml);
  await attachInitSegments(manifest, fetchInitSegment);
  return validateManifest(manifest, options);
}

async function attachInitSegments(
  manifest: ParsedMpd,
  fetchInitSegment: (id: string) => Promise<Uint8Array | ArrayBuffer> | Uint8Array | ArrayBuffer,
): Promise<void> {
  const seen = new Map<string, Uint8Array>();
  for (const adaptationSet of manifest.adaptationSets) {
    for (const representation of adaptationSet.representations) {
      if (!seen.has(representation.id)) {
        const segment = await fetchInitSegment(representation.id);
        seen.set(
          representation.id,
          segment instanceof Uint8Array ? segment : new Uint8Array(segment),
        );
      }
      representation.initSegment = seen.get(representation.id);
    }
  }
}

export { parseMpd } from './mpd.ts';
export { parseInitSegment } from './mp4.ts';
export { validateManifest, extractKidsFromPssh, extractKidsFromMpdPssh } from './validator.ts';
export { checkLicenseUrl } from './license.ts';
export { normalizeKid, normalizeKidList, bytesToKid, kidToBytes, kidEquals } from './kid.ts';
export { base64ToBytes } from './base64.ts';
export type {
  AdaptationSet,
  ContentProtection,
  NormalizedKid,
  ParsedInitSegment,
  ParsedMpd,
  PsshBox,
  Representation,
  SchemeType,
  TrackProtection,
  ValidationCode,
  ValidationIssue,
  ValidationOptions,
  ValidationResult,
} from './types.ts';
