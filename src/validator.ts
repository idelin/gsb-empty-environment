import type {
  AdaptationSet,
  ContentProtection,
  NormalizedKid,
  ParsedInitSegment,
  ParsedMpd,
  Representation,
  SchemeType,
  ValidationIssue,
  ValidationOptions,
  ValidationResult,
} from './types.ts';
import { normalizeKid } from './kid.ts';
import { parseInitSegment } from './mp4.ts';
import { base64ToBytes } from './base64.ts';
import { checkLicenseUrl } from './license.ts';

/** Widevine SystemID（用于厂商私有 PSSH data 的 KID 提取）。 */
const WIDEVINE_SYSTEM_ID = 'edef8ba979d64acea3c827dcd51d21ed';

/** DASH-IF 约定：CENC 方案通过该 URN 的 `value` 属性声明。 */
const CENC_SCHEME_URI = 'urn:uuid:1077efec-c022-4bdf-9047-cc25718c72fe';

function effectiveContentProtections(
  adaptationSet: AdaptationSet,
  representation: Representation,
): ContentProtection[] {
  return representation.contentProtections.length
    ? representation.contentProtections
    : adaptationSet.contentProtections;
}

function collectDefaultKids(cps: ContentProtection[]): {
  kids: NormalizedKid[];
  malformed: string[];
} {
  const kids: NormalizedKid[] = [];
  const malformed: string[] = [];
  for (const cp of cps) {
    for (const raw of cp.defaultKids) {
      const normalized = normalizeKid(raw);
      if (normalized) {
        if (!kids.includes(normalized)) kids.push(normalized);
      } else {
        malformed.push(raw);
      }
    }
  }
  return { kids, malformed };
}

function extractMpdScheme(cps: ContentProtection[]): SchemeType | undefined {
  for (const cp of cps) {
    if (cp.schemeIdUri?.toLowerCase() === CENC_SCHEME_URI && cp.value) {
      return cp.value.trim().toLowerCase();
    }
  }
  return undefined;
}

/**
 * 从 PSSH 盒子中收集 KID：
 * - 标准 v1 盒子直接读取 KID 数组；
 * - v0 Widevine 盒子解析 protobuf `field 2 = repeated bytes KID`。
 */
export function extractKidsFromPssh(
  init: ParsedInitSegment,
): Map<NormalizedKid, string> {
  const found = new Map<NormalizedKid, string>();
  for (const box of init.psshBoxes) {
    for (const kid of box.kids) found.set(kid, box.locator);
    if (box.version === 0 && box.systemId === WIDEVINE_SYSTEM_ID) {
      for (const kid of parseWidevineKids(box.raw, box.dataOffset)) {
        found.set(kid, box.locator);
      }
    }
  }
  return found;
}

/**
 * 解析 Widevine PsshContainer protobuf：
 * message { repeated bytes KeyId = 2; ... }
 * 仅做最小 wire-format 扫描，不引入 protobuf 依赖。
 */
function parseWidevineKids(boxBytes: Uint8Array, dataOffset: number): NormalizedKid[] {
  const kids: NormalizedKid[] = [];
  let i = dataOffset;
  const end = boxBytes.length;
  const readVarint = (): number => {
    let value = 0;
    let shift = 0;
    while (i < end && shift < 32) {
      const byte = boxBytes[i++];
      value |= (byte & 0x7f) << shift;
      if ((byte & 0x80) === 0) return value >>> 0;
      shift += 7;
    }
    return -1;
  };

  while (i < end) {
    const tag = readVarint();
    if (tag < 0) break;
    const fieldNumber = tag >> 3;
    const wireType = tag & 0x07;
    if (wireType === 2) {
      const length = readVarint();
      if (length < 0 || i + length > end) break;
      if (fieldNumber === 2 && length === 16) {
        let hex = '';
        for (let j = 0; j < 16; j++) {
          hex += boxBytes[i + j].toString(16).padStart(2, '0');
        }
        kids.push(hex);
      }
      i += length;
    } else if (wireType === 0) {
      if (readVarint() < 0) break;
    } else {
      break;
    }
  }
  return kids;
}

function validateAdaptationSet(
  adaptationSet: AdaptationSet,
  options: ValidationOptions,
): ValidationIssue[] {
  const issues: ValidationIssue[] = [];
  const locator = adaptationSet.locator ?? 'AdaptationSet';

  if (!adaptationSet.representations.length) {
    issues.push({
      code: 'ADAPTATION_SET_EMPTY',
      message: 'AdaptationSet 不包含任何 Representation',
      locator,
    });
    return issues;
  }

  const allCps: ContentProtection[] = [...adaptationSet.contentProtections];
  const licensedCps = new Set<ContentProtection>();
  const repDeclaredKid = new Map<string, NormalizedKid[]>();

  for (const representation of adaptationSet.representations) {
    const cps = effectiveContentProtections(adaptationSet, representation);
    allCps.push(...cps);
    for (const cp of cps) licensedCps.add(cp);
    if (!cps.length) {
      issues.push({
        code: 'NO_CONTENT_PROTECTION',
        message: 'Representation 及其 AdaptationSet 均未声明 ContentProtection',
        locator: representation.locator,
      });
      continue;
    }
    const { kids, malformed } = collectDefaultKids(cps);
    repDeclaredKid.set(representation.id, kids);
    if (malformed.length) {
      issues.push({
        code: 'DEFAULT_KID_MALFORMED',
        message: `default_KID 不是合法 UUID/十六进制: ${malformed.join(', ')}`,
        locator: representation.locator,
      });
    }
    if (!kids.length) {
      issues.push({
        code: 'DEFAULT_KID_MISSING',
        message: 'ContentProtection 未声明 cenc:default_KID',
        locator: representation.locator,
      });
    }
  }

  // 规则 1：同一 AdaptationSet 内各 Representation 的 default_KID 必须一致。
  const uniqueMpdKids = new Set<NormalizedKid>();
  for (const kids of repDeclaredKid.values()) {
    for (const kid of kids) uniqueMpdKids.add(kid);
  }
  if (uniqueMpdKids.size > 1) {
    issues.push({
      code: 'DEFAULT_KID_MISMATCH',
      message: `同一 AdaptationSet 内 default_KID 不一致: ${[...uniqueMpdKids].join(', ')}`,
      locator,
    });
  }
  const mpdKid = uniqueMpdKids.size === 1 ? [...uniqueMpdKids][0] : undefined;

  const mpdScheme = extractMpdScheme(allCps) ?? options.expectedScheme?.toLowerCase();

  // 规则 2/3/4：逐 Representation 校验初始化分段 tenc / schm / PSSH。
  for (const representation of adaptationSet.representations) {
    validateRepresentationSegment(
      representation,
      mpdKid,
      mpdScheme,
      options,
      effectiveContentProtections(adaptationSet, representation),
      issues,
    );
  }

  // 规则 5：licenseUrl 白名单（AdaptationSet 与 Representation 两级都查）。
  for (const cp of licensedCps) {
    const result = checkLicenseUrl(
      cp.licenseUrl,
      options.allowedLicenseUrls,
      options.allowNonDefaultPort,
      cp.locator,
    );
    if (!result.ok && result.issue) issues.push(result.issue);
  }

  return issues;
}

function validateRepresentationSegment(
  representation: Representation,
  mpdKid: NormalizedKid | undefined,
  mpdScheme: SchemeType | undefined,
  options: ValidationOptions,
  cps: ContentProtection[],
  issues: ValidationIssue[],
): void {
  const locator = representation.locator ?? `Representation[${representation.id}]`;
  if (!representation.initSegment) {
    issues.push({
      code: 'INIT_SEGMENT_MISSING',
      message: '缺少用于校验的初始化分段字节',
      locator,
    });
    return;
  }

  const init = parseInitSegment(representation.initSegment);
  if (!init.tracks.length) {
    issues.push({
      code: 'TENC_MISSING',
      message: '初始化分段未找到 tenc（sinf）盒子',
      locator,
    });
    return;
  }

  const tencKids = new Set(init.tracks.map((track) => track.kid));
  for (const track of init.tracks) {
    // 规则 2：MPD default_KID 必须等于 tenc KID。
    if (mpdKid && track.kid !== mpdKid) {
      issues.push({
        code: 'TENC_KID_MISMATCH',
        message: `tenc KID ${track.kid} 与 MPD default_KID ${mpdKid} 不一致`,
        locator: track.locator,
        kid: track.kid,
      });
    }

    // 规则 3：方案值必须与 tenc 同轨道的 schm 一致。
    const effectiveScheme = mpdScheme ?? options.expectedScheme?.toLowerCase();
    if (!effectiveScheme) {
      issues.push({
        code: 'SCHEME_MISSING',
        message: 'MPD 未声明 CENC 方案且未提供 expectedScheme，无法比对 schm',
        locator,
      });
    } else if (!track.schemeType) {
      issues.push({
        code: 'SCHEME_MISSING',
        message: 'sinf 内缺少 schm 盒子，无法确认加密方案',
        locator: track.locator,
      });
    } else if (track.schemeType.toLowerCase() !== effectiveScheme) {
      issues.push({
        code: 'SCHEME_MISMATCH',
        message: `方案不一致: MPD=${effectiveScheme} tenc/schm=${track.schemeType}`,
        locator: track.locator,
        kid: track.kid,
      });
    }
  }

  // 规则 4：PSSH 必须存在且包含该 KID。
  validatePssh(init, tencKids, locator, cps, options, issues);
}

function validatePssh(
  init: ParsedInitSegment,
  tencKids: Set<NormalizedKid>,
  locator: string,
  cps: ContentProtection[],
  options: ValidationOptions,
  issues: ValidationIssue[],
): void {
  if (!init.psshBoxes.length) {
    issues.push({
      code: 'PSSH_MISSING',
      message: '初始化分段未包含任何 PSSH 盒子',
      locator,
    });
    return;
  }

  const psshKids = extractKidsFromPssh(init);
  for (const kid of tencKids) {
    if (!psshKids.has(kid)) {
      issues.push({
        code: 'PSSH_KID_MISSING',
        message: `PSSH 盒子中未找到 tenc KID ${kid}`,
        locator,
        kid,
      });
    }
  }

  if (options.requireMpdPsshKid) {
    const inlineBoxes = cps.flatMap((cp) => cp.psshBoxesBase64);
    if (inlineBoxes.length) {
      const inlineKids = new Set(extractKidsFromMpdPssh(inlineBoxes));
      for (const kid of tencKids) {
        if (!inlineKids.has(kid)) {
          issues.push({
            code: 'PSSH_KID_MISSING',
            message: `MPD 内嵌 PSSH 中未找到 tenc KID ${kid}`,
            locator,
            kid,
          });
        }
      }
    }
  }
}

/** 对已解析模型执行全部校验。 */
export function validateManifest(
  manifest: ParsedMpd,
  options: ValidationOptions,
): ValidationResult {
  const issues: ValidationIssue[] = [];
  for (const adaptationSet of manifest.adaptationSets) {
    issues.push(...validateAdaptationSet(adaptationSet, options));
  }
  return { ok: issues.length === 0, issues };
}

/** 解析 MPD 内嵌 Base64 PSSH 并返回其中出现的 KID 集合。 */
export function extractKidsFromMpdPssh(base64Boxes: string[]): NormalizedKid[] {
  const kids: NormalizedKid[] = [];
  for (const base64 of base64Boxes) {
    const bytes = base64ToBytes(base64);
    const parsed = parseInitSegment(bytes);
    for (const kid of extractKidsFromPssh(parsed).keys()) {
      if (!kids.includes(kid)) kids.push(kid);
    }
  }
  return kids;
}
