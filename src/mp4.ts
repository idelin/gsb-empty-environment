import type { ParsedInitSegment, PsshBox, TrackProtection } from './types.ts';
import { bytesToKid } from './kid.ts';

const TEXT_DECODER = new TextDecoder('latin1');

function readType(data: Uint8Array, offset: number): string {
  return TEXT_DECODER.decode(data.subarray(offset, offset + 4));
}

function readU32(data: Uint8Array, offset: number): number {
  return (
    (data[offset] * 0x1000000) +
    ((data[offset + 1] << 16) | (data[offset + 2] << 8) | data[offset + 3])
  );
}

/**
 * 容器盒子集合：这些盒子的载荷直接由子盒子构成。
 * 同时包含加密/普通样本条目类型，便于递归进入 `stsd -> encv/enca -> sinf`。
 */
const CONTAINER_BOXES = new Set([
  'moov', 'trak', 'mdia', 'minf', 'stbl', 'stsd', 'edts', 'dinf',
  'mvex', 'moof', 'traf', 'mfra', 'sinf', 'wave', 'skip', 'udta',
]);

/**
 * 样本条目类型：位于 `stsd` 内，盒子头之后先有 8 字节固定字段
 * （6 字节 reserved + data_reference_index），音频条目还会继续带
 * 音频头；其加密子盒子 `sinf` 是固定头之后的最后一个子盒子。
 */
const SAMPLE_ENTRY_BOXES = new Set([
  'encv', 'enca', 'encs', 'avc1', 'avc3', 'hvc1', 'hev1', 'mp4a',
  'ac-3', 'ec-3', 'Opus', 'vp08', 'vp09', 'av01', 'resv',
]);

interface BoxHeader {
  type: string;
  /** 盒子起始偏移（含 size/type 头）。 */
  start: number;
  /** 盒子头之后的载荷偏移。 */
  payloadStart: number;
  /** 盒子整体结束偏移（不含）。 */
  end: number;
}

function* iterBoxes(
  data: Uint8Array,
  start: number,
  end: number,
): Generator<BoxHeader> {
  let offset = start;
  while (offset + 8 <= end) {
    let size = readU32(data, offset);
    const type = readType(data, offset + 4);
    let headerLength = 8;
    if (size === 1) {
      if (offset + 16 > end) return;
      const high = readU32(data, offset + 8);
      const low = readU32(data, offset + 12);
      size = high * 0x100000000 + low;
      headerLength = 16;
    } else if (size === 0) {
      size = end - offset;
    }
    if (size < headerLength || offset + size > end) return;
    yield { start: offset, type, payloadStart: offset + headerLength, end: offset + size };
    offset += size;
  }
}

function parsePssh(data: Uint8Array, box: BoxHeader): PsshBox | null {
  const p = box.payloadStart;
  if (p + 4 + 16 > box.end) return null;
  const version = data[p];
  const systemIdBytes = data.subarray(p + 4, p + 20);
  const systemId = bytesToKid(systemIdBytes);
  if (!systemId) return null;
  const kids: string[] = [];
  let cursor = p + 20;
  if (version === 1) {
    if (cursor + 4 > box.end) return null;
    const count = readU32(data, cursor);
    cursor += 4;
    for (let i = 0; i < count; i++) {
      if (cursor + 16 > box.end) return null;
      const kid = bytesToKid(data.subarray(cursor, cursor + 16));
      if (kid) kids.push(kid);
      cursor += 16;
    }
  }
  return {
    systemId,
    version,
    kids,
    raw: data.subarray(box.start, box.end),
    dataOffset: cursor,
    locator: `pssh[${systemId}]v${version}`,
  };
}

function parseTenc(
  data: Uint8Array,
  payloadStart: number,
  end: number,
  locator: string,
): TrackProtection | null {
  // FullBox: version(1) flags(3)，随后 reserved/isProtected(1) +
  // per_sample_iv_size(1) + KID(16)
  const p = payloadStart + 4;
  if (p + 18 > end) return null;
  const isProtected = (data[p] & 0x80) === 0x80;
  const perSampleIvSize = data[p + 1];
  const kidBytes = data.slice(p + 2, p + 18);
  const kid = bytesToKid(kidBytes);
  if (!kid) return null;
  return {
    kidBytes,
    kid,
    isProtected,
    perSampleIvSize,
    locator,
  };
}

function parseSchm(data: Uint8Array, payloadStart: number, end: number): string | undefined {
  // FullBox 头 4 字节 + scheme_type 4 字节
  const p = payloadStart + 4;
  if (p + 4 > end) return undefined;
  return TEXT_DECODER.decode(data.subarray(p, p + 4));
}

/**
 * 解析 CENC 初始化分段，收集全部 `tenc`（经 sinf）与 `pssh` 盒子。
 *
 * 解析是防御性的：截断或异常盒子会被跳过而不是抛异常，保证被篡改的
 * 分段也能产出结构化结果，交由校验逻辑报告缺失项。
 */
export function parseInitSegment(input: Uint8Array | ArrayBuffer): ParsedInitSegment {
  const data = input instanceof Uint8Array ? input : new Uint8Array(input);
  const tracks: TrackProtection[] = [];
  const psshBoxes: PsshBox[] = [];

  const walk = (start: number, end: number, path: string): void => {
    for (const box of iterBoxes(data, start, end)) {
      const here = path ? `${path}/${box.type}` : box.type;
      if (box.type === 'pssh') {
        const pssh = parsePssh(data, box);
        if (pssh) {
          pssh.locator = `${here}:${pssh.locator}`;
          psshBoxes.push(pssh);
        }
      } else if (box.type === 'sinf') {
        let track: TrackProtection | null = null;
        let schemeType: string | undefined;
        for (const child of iterBoxes(data, box.payloadStart, box.end)) {
          if (child.type === 'tenc') {
            track = parseTenc(data, child.payloadStart, child.end, `${here}/tenc`);
          } else if (child.type === 'schm') {
            schemeType = parseSchm(data, child.payloadStart, child.end);
          }
        }
        if (track) {
          track.schemeType = schemeType;
          tracks.push(track);
        }
      } else if (box.type === 'stsd') {
        // stsd: FullBox(4) + entry_count(4)，其后逐个为样本条目。
        walk(box.payloadStart + 8, box.end, here);
      } else if (SAMPLE_ENTRY_BOXES.has(box.type)) {
        // 跳过样本条目 8 字节固定头，递归其内嵌的 sinf 等子盒子。
        walk(box.payloadStart + 8, box.end, here);
      } else if (CONTAINER_BOXES.has(box.type)) {
        walk(box.payloadStart, box.end, here);
      }
    }
  };

  walk(0, data.length, '');
  return { tracks, psshBoxes };
}
