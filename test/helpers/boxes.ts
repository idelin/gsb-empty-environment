/**
 * 测试用 ISO-BMFF 盒子构造器，生成结构合法但最小化的初始化分段，
 * 让解析/校验逻辑无需依赖真实媒体文件即可覆盖攻击场景。
 */

export function box(type: string, payload: Uint8Array = new Uint8Array(0)): Uint8Array {
  const out = new Uint8Array(8 + payload.length);
  const view = new DataView(out.buffer);
  view.setUint32(0, out.length);
  for (let i = 0; i < 4; i++) out[4 + i] = type.charCodeAt(i);
  out.set(payload, 8);
  return out;
}

export function concat(...parts: Uint8Array[]): Uint8Array {
  const total = parts.reduce((sum, part) => sum + part.length, 0);
  const out = new Uint8Array(total);
  let offset = 0;
  for (const part of parts) {
    out.set(part, offset);
    offset += part.length;
  }
  return out;
}

const FULLBOX_ZERO = new Uint8Array([0, 0, 0, 0]);

export function tencBox(kid: Uint8Array, perSampleIvSize = 8): Uint8Array {
  const payload = new Uint8Array(4 + 1 + 1 + 16);
  payload.set(FULLBOX_ZERO, 0);
  payload[4] = 0x81; // reserved 0x80 | isProtected=1
  payload[5] = perSampleIvSize;
  payload.set(kid, 6);
  return box('tenc', payload);
}

export function schmBox(schemeType: string, schemeVersion = 0x00010000): Uint8Array {
  const payload = new Uint8Array(4 + 4 + 4);
  payload.set(FULLBOX_ZERO, 0);
  for (let i = 0; i < 4; i++) payload[4 + i] = schemeType.charCodeAt(i);
  new DataView(payload.buffer).setUint32(8, schemeVersion);
  return box('schm', payload);
}

export function frmaBox(dataFormat = 'mp4a'): Uint8Array {
  const payload = new Uint8Array(4);
  for (let i = 0; i < 4; i++) payload[i] = dataFormat.charCodeAt(i);
  return box('frma', payload);
}

export function sinfBox(kid: Uint8Array, schemeType = 'cenc'): Uint8Array {
  return box('sinf', concat(frmaBox(), schmBox(schemeType), tencBox(kid)));
}

export function encvBox(sinf: Uint8Array): Uint8Array {
  // 样本条目固定头：6 字节保留 + data_reference_index(2)，随后为子盒子。
  const fixedHeader = new Uint8Array(8);
  return box('encv', concat(fixedHeader, sinf));
}

export function stsdBox(entries: Uint8Array[]): Uint8Array {
  const header = new Uint8Array(8);
  new DataView(header.buffer).setUint32(4, entries.length);
  return box('stsd', concat(header, ...entries));
}

export function stblBox(...children: Uint8Array[]): Uint8Array {
  return box('stbl', concat(...children));
}

export function minfBox(stbl: Uint8Array): Uint8Array {
  return box('minf', stbl);
}

export function mdiaBox(minf: Uint8Array): Uint8Array {
  return box('mdia', minf);
}

export function trakBox(mdia: Uint8Array): Uint8Array {
  return box('trak', mdia);
}

export function moovBox(...traks: Uint8Array[]): Uint8Array {
  return box('moov', concat(...traks));
}

export function initSegmentWithSinf(kid: Uint8Array, schemeType = 'cenc'): Uint8Array {
  const sinf = sinfBox(kid, schemeType);
  const stbl = stblBox(stsdBox([encvBox(sinf)]));
  return moovBox(trakBox(mdiaBox(minfBox(stbl))));
}

export function psshV1(systemId: Uint8Array, kids: Uint8Array[]): Uint8Array {
  const payload = new Uint8Array(4 + 16 + 4 + kids.length * 16);
  payload[0] = 1; // version=1, flags=0
  payload.set(systemId, 4);
  new DataView(payload.buffer).setUint32(20, kids.length);
  let offset = 24;
  for (const kid of kids) {
    payload.set(kid, offset);
    offset += 16;
  }
  return box('pssh', payload);
}

export function psshV0(systemId: Uint8Array, data: Uint8Array): Uint8Array {
  const payload = new Uint8Array(4 + 16 + 4 + data.length);
  payload.set(systemId, 4);
  new DataView(payload.buffer).setUint32(20, data.length);
  payload.set(data, 24);
  return box('pssh', payload);
}

/** 构造 Widevine PsshContainer 中 field 2 的 16 字节 KID 条目。 */
export function widevineKidField(kid: Uint8Array): Uint8Array {
  // tag = (field 2 << 3) | wire type 2 = 0x12；length=16
  return concat(new Uint8Array([0x12, 0x10]), kid);
}

export function ftypBox(): Uint8Array {
  return box('ftyp', new TextEncoder().encode('iso5'));
}
