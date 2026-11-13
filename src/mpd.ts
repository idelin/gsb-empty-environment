import type {
  AdaptationSet,
  ContentProtection,
  ParsedMpd,
  Representation,
} from './types.ts';
import { findChildren, parseXml, type XmlNode } from './xml.ts';

/**
 * 常见携带许可证 URL 的属性名（按本地名匹配，命名空间无关）。
 * 不同 DRM/清单规范命名不一，这里做防御性兼容。
 */
const LICENSE_ATTR_NAMES = new Set([
  'licenseurl',
  'laurl',
  'licenseacquisitionurl',
  'dashif:laurl',
]);

function extractLicenseUrl(cp: XmlNode): string | undefined {
  for (const [key, value] of Object.entries(cp.attrs)) {
    const lower = key.toLowerCase();
    if (LICENSE_ATTR_NAMES.has(lower) || lower.endsWith(':licenseurl')) {
      return value;
    }
  }
  for (const child of cp.children) {
    if (child.tag === 'laurl' && child.attrs.href) return child.attrs.href;
  }
  return undefined;
}

function toContentProtection(node: XmlNode, locator: string): ContentProtection {
  const defaultKids: string[] = [];
  const psshBoxesBase64: string[] = [];

  for (const [key, value] of Object.entries(node.attrs)) {
    if (key.toLowerCase().endsWith(':default_kid') || key.toLowerCase() === 'default_kid') {
      for (const token of value.split(/\s+/)) {
        if (token) defaultKids.push(token);
      }
    }
  }

  for (const child of node.children) {
    if (child.tag === 'pssh' && child.text.trim()) {
      psshBoxesBase64.push(child.text.trim());
    }
  }

  return {
    schemeIdUri: node.attrs.schemeiduri,
    value: node.attrs.value,
    defaultKids,
    licenseUrl: extractLicenseUrl(node),
    psshBoxesBase64,
    locator,
  };
}

function toRepresentation(node: XmlNode, parentLocator: string): Representation {
  const id = node.attrs.id ?? 'unknown';
  const locator = `${parentLocator}/Representation[@id="${id}"]`;
  const contentProtections = findChildren(node, 'contentprotection').map((cp, idx) =>
    toContentProtection(cp, `${locator}/ContentProtection[${idx}]`),
  );
  return { id, contentProtections, locator };
}

function toAdaptationSet(node: XmlNode, index: number): AdaptationSet {
  const id = node.attrs.id;
  const locator = id !== undefined
    ? `AdaptationSet[@id="${id}"]`
    : `AdaptationSet[${index}]`;
  const contentProtections = findChildren(node, 'contentprotection').map((cp, idx) =>
    toContentProtection(cp, `${locator}/ContentProtection[${idx}]`),
  );
  const representations = findChildren(node, 'representation').map((rep) =>
    toRepresentation(rep, locator),
  );
  return {
    id,
    contentType: node.attrs.contenttype,
    contentProtections,
    representations,
    locator,
  };
}

/** 解析 MPD 文本为校验包使用的中立模型。 */
export function parseMpd(mpdXml: string): ParsedMpd {
  const doc = parseXml(mpdXml);
  const mpd = doc.children.find((node) => node.tag === 'mpd') ?? doc;
  const periods = findChildren(mpd, 'period');
  const adaptationSets: AdaptationSet[] = [];
  const periodList = periods.length ? periods : [mpd];
  periodList.forEach((period, periodIdx) => {
    findChildren(period, 'adaptationset').forEach((asNode, asIdx) => {
      const as = toAdaptationSet(asNode, asIdx);
      if (periods.length) {
        as.locator = `Period[${periodIdx}]/${as.locator}`;
      }
      adaptationSets.push(as);
    });
  });
  return { adaptationSets };
}
