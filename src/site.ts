/**
 * Schemeful Same-Site 判定与浏览上下文树工具。
 *
 * Schemeful 规则：scheme 不同即跨站，因此 http://a.example 与
 * https://a.example 视为跨站；端口不参与站点键。
 */

import type { AncestorEntry, FrameNode, Origin, SiteKey } from './types.ts';
import { normalizeHost, type PslResolution } from './psl.ts';

export interface ParsedUrl {
  readonly scheme: string;
  readonly host: string;
  readonly port: number | null;
  readonly path: string;
  readonly url: string;
}

export function parseUrl(raw: string): ParsedUrl {
  const url = new URL(raw);
  if (url.protocol !== 'http:' && url.protocol !== 'https:') {
    throw new Error(`unsupported non-http(s) url: ${raw}`);
  }
  return {
    scheme: url.protocol.slice(0, -1),
    host: normalizeHost(url.hostname),
    port: url.port ? Number(url.port) : null,
    path: url.pathname || '/',
    url: url.toString(),
  };
}

export function siteKeyOf(parsed: ParsedUrl, resolution: PslResolution): SiteKey {
  return { scheme: parsed.scheme, registrableDomain: resolution.registrableDomain };
}

/** 两个站点键是否同站；任一不透明身份均按跨站处理（不同 nonce 永不等价）。 */
export function sameSite(a: SiteKey | null, b: SiteKey | null): boolean {
  if (a === null || b === null) {
    return false;
  }
  return a.scheme === b.scheme && a.registrableDomain === b.registrableDomain;
}

/** 不透明来源与框架来源的跨站判定。 */
export function originsCrossSite(
  origin: Origin,
  reference: SiteKey,
  resolve: (host: string) => PslResolution,
): boolean {
  if (origin.opaque) {
    return true;
  }
  if (origin.scheme !== reference.scheme) {
    return true;
  }
  const key: SiteKey = {
    scheme: origin.scheme,
    registrableDomain: resolve(origin.host).registrableDomain,
  };
  return !sameSite(key, reference);
}

/**
 * 祖先来源链是否全部同站。
 * 任一不透明身份（sandbox 去源）或站点不一致即跨站。
 */
export function ancestorChainSameSite(
  chain: readonly AncestorEntry[],
  reference: SiteKey,
  resolve: (host: string) => PslResolution,
): boolean {
  for (const entry of chain) {
    if (originsCrossSite(entry.effectiveOrigin, reference, resolve)) {
      return false;
    }
  }
  return true;
}

/** 校验 FrameNode 的 sandbox 不变量并抽取祖先来源链。 */
export function ancestorChainFromTree(root: FrameNode, targetFrameId: string): AncestorEntry[] {
  const path: FrameNode[] = [];
  const found = walk(root, targetFrameId, path);
  if (!found) {
    throw new Error(`frame not found in context tree: ${targetFrameId}`);
  }
  return path.map((node) => {
    if (node.sandboxed && !node.allowSameOrigin && !node.effectiveOrigin.opaque) {
      throw new Error(`sandbox invariant violated: frame ${node.id} must have an opaque origin`);
    }
    if (node.effectiveOrigin.opaque && node.allowSameOrigin && node.sandboxed) {
      throw new Error(`sandbox invariant violated: allow-same-origin frame ${node.id} cannot be opaque`);
    }
    return {
      frameId: node.id,
      effectiveOrigin: node.effectiveOrigin,
      sandboxed: node.sandboxed,
      allowSameOrigin: node.allowSameOrigin,
    };
  });
}

function walk(node: FrameNode, target: string, path: FrameNode[]): boolean {
  path.push(node);
  if (node.id === target) {
    return true;
  }
  for (const child of node.children) {
    if (walk(child, target, path)) {
      return true;
    }
  }
  path.pop();
  return false;
}

/** 顶层框架的分区键；顶层为不透明来源时返回 null（分区 Cookie 不可用）。 */
export function topLevelPartitionKey(
  chain: readonly AncestorEntry[],
  resolve: (host: string) => PslResolution,
): SiteKey | null {
  const top = chain[0];
  if (!top || top.effectiveOrigin.opaque) {
    return null;
  }
  const origin = top.effectiveOrigin;
  return {
    scheme: origin.scheme,
    registrableDomain: resolve(origin.host).registrableDomain,
  };
}
