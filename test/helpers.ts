import {
  encodeMessage,
  type Disclosure,
  type ErrorNode,
  type ViewerContext,
} from '../src/index.ts';

export const KEY = 'test-only-egress-key';

export const text = (s: string): Uint8Array => new TextEncoder().encode(s);

/** 构造一个原始消息体：[字段索引, 字节] 列表。 */
export const raw = (...parts: Array<readonly [number, Uint8Array]>): Uint8Array =>
  encodeMessage(parts);

export const PUB: Disclosure = { kind: 'public' };
export const TENANT: Disclosure = { kind: 'tenant' };
export const restricted = (level: number): Disclosure => ({ kind: 'restricted', level });

export function makeNode(init: {
  service: string;
  tenant: string;
  typeUrl?: string;
  raw?: Uint8Array;
  visibility?: Disclosure;
  disclose?: Record<string, Disclosure>;
  causes?: ErrorNode[];
  details?: ErrorNode[];
}): ErrorNode {
  return {
    service: init.service,
    tenant: init.tenant,
    typeUrl: init.typeUrl ?? 'svc/Error',
    raw: init.raw ?? new Uint8Array(),
    visibility: init.visibility ?? PUB,
    disclose: init.disclose ?? {},
    causes: init.causes ?? [],
    details: init.details ?? [],
  };
}

export const viewer = (
  tenant: string,
  clearance = 0,
  generation = 1,
): ViewerContext => ({ tenant, clearance, generation });

/** 组装规范文本：帧数组 + 每帧一个换行。 */
export const frames = (...lines: string[]): string =>
  lines.map((line) => `${line}\n`).join('');
