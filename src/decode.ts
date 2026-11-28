import { XmlSecurityError } from './errors.ts';

export type WhitelistedEncoding = 'UTF-8' | 'UTF-16LE' | 'UTF-16BE';

export interface DecodeResult {
  /** 已剥离 BOM 的 Unicode 文本。 */
  text: string;
  /** 由 BOM 或字节序嗅探得到的实际编码。 */
  encoding: WhitelistedEncoding;
  /** 输入中是否存在 BOM。 */
  hadBom: boolean;
  /** 声明中的编码（若存在），保留规范化后的大写形式。 */
  declaredEncoding?: string;
}

const UTF8_BOM = [0xef, 0xbb, 0xbf];
const UTF16LE_BOM = [0xff, 0xfe];
const UTF16BE_BOM = [0xfe, 0xff];

function hasPrefix(bytes: Uint8Array, prefix: number[]): boolean {
  if (bytes.length < prefix.length) {
    return false;
  }
  return prefix.every((byte, index) => bytes[index] === byte);
}

/**
 * 无 BOM 时按 XML 附录 F 的四字节模型区分 UTF-8 / UTF-16LE / UTF-16BE。
 */
function sniffEncoding(bytes: Uint8Array): WhitelistedEncoding {
  if (bytes.length >= 2 && bytes[0] === 0 && bytes[1] !== 0) {
    return 'UTF-16BE';
  }
  if (bytes.length >= 2 && bytes[0] !== 0 && bytes[1] === 0) {
    return 'UTF-16LE';
  }
  if (bytes.length >= 4 && bytes[0] === 0 && bytes[1] === 0x3c && bytes[2] === 0 && bytes[3] === 0x3f) {
    return 'UTF-16BE';
  }
  if (bytes.length >= 4 && bytes[0] === 0x3c && bytes[1] === 0 && bytes[2] === 0x3f && bytes[3] === 0) {
    return 'UTF-16LE';
  }
  return 'UTF-8';
}

function normalizeEncodingName(raw: string): string {
  return raw.trim().toUpperCase().replace(/_/g, '-');
}

const ENCODING_ALIASES: Record<string, WhitelistedEncoding> = {
  'UTF-8': 'UTF-8',
  'UTF8': 'UTF-8',
  'UCS-2': 'UTF-16LE',
  'UCS2': 'UTF-16LE',
  'UTF-16': 'UTF-16LE',
  'UTF16': 'UTF-16LE',
  'ISO-10646-UCS-2': 'UTF-16LE',
  'ISO-10646-UCS-4': 'UTF-16LE',
  'UTF-16LE': 'UTF-16LE',
  'UTF-16BE': 'UTF-16BE',
};

function readXmlDeclarationEncoding(text: string): string | undefined {
  if (!text.startsWith('<?xml')) {
    return undefined;
  }
  const close = text.indexOf('?>');
  if (close < 0 || close > 200) {
    throw new XmlSecurityError('DECLARATION_MALFORMED', 'XML declaration is malformed or unterminated');
  }
  const declaration = text.slice(5, close);
  const match = /\s+encoding\s*=\s*(?:"([^"]*)"|'([^']*)')/.exec(declaration);
  if (!match) {
    return undefined;
  }
  const value = match[1] ?? match[2] ?? '';
  if (value.length === 0) {
    throw new XmlSecurityError('DECLARATION_MALFORMED', 'empty encoding pseudo-attribute');
  }
  return normalizeEncodingName(value);
}

/**
 * 严格 UTF-8 解码：拒绝超长编码、代理码点与所有非法字节序列。
 */
export function decodeUtf8(bytes: Uint8Array): string {
  const codePoints: number[] = [];
  let i = 0;
  while (i < bytes.length) {
    const b0 = bytes[i] as number;
    let cp: number;
    let width: number;
    if (b0 < 0x80) {
      cp = b0;
      width = 1;
    } else if ((b0 & 0xe0) === 0xc0) {
      width = 2;
      if (b0 < 0xc2) {
        throw new XmlSecurityError('ENCODING_MALFORMED', 'overlong UTF-8 lead byte', i);
      }
      const b1 = bytes[i + 1];
      if (i + 1 >= bytes.length || (b1 & 0xc0) !== 0x80) {
        throw new XmlSecurityError('ENCODING_MALFORMED', 'truncated UTF-8 continuation byte', i);
      }
      cp = ((b0 & 0x1f) << 6) | (b1 & 0x3f);
    } else if ((b0 & 0xf0) === 0xe0) {
      width = 3;
      const b1 = bytes[i + 1];
      const b2 = bytes[i + 2];
      if (i + 2 >= bytes.length || (b1 & 0xc0) !== 0x80 || (b2 & 0xc0) !== 0x80) {
        throw new XmlSecurityError('ENCODING_MALFORMED', 'truncated UTF-8 continuation byte', i);
      }
      if (b0 === 0xe0 && b1 < 0xa0) {
        throw new XmlSecurityError('ENCODING_MALFORMED', 'overlong UTF-8 sequence', i);
      }
      if (b0 === 0xed && b1 >= 0xa0) {
        throw new XmlSecurityError('ENCODING_MALFORMED', 'UTF-8 surrogate code point', i);
      }
      cp = ((b0 & 0x0f) << 12) | ((b1 & 0x3f) << 6) | (b2 & 0x3f);
    } else if ((b0 & 0xf8) === 0xf0) {
      width = 4;
      const b1 = bytes[i + 1];
      const b2 = bytes[i + 2];
      const b3 = bytes[i + 3];
      if (
        i + 3 >= bytes.length ||
        (b1 & 0xc0) !== 0x80 ||
        (b2 & 0xc0) !== 0x80 ||
        (b3 & 0xc0) !== 0x80
      ) {
        throw new XmlSecurityError('ENCODING_MALFORMED', 'truncated UTF-8 continuation byte', i);
      }
      if (b0 === 0xf0 && b1 < 0x90) {
        throw new XmlSecurityError('ENCODING_MALFORMED', 'overlong UTF-8 sequence', i);
      }
      if (b0 > 0xf4 || (b0 === 0xf4 && b1 >= 0x90)) {
        throw new XmlSecurityError('ENCODING_MALFORMED', 'Unicode code point above U+10FFFF', i);
      }
      cp = ((b0 & 0x07) << 18) | ((b1 & 0x3f) << 12) | ((b2 & 0x3f) << 6) | (b3 & 0x3f);
    } else {
      throw new XmlSecurityError('ENCODING_MALFORMED', 'invalid UTF-8 lead byte', i);
    }
    codePoints.push(cp);
    i += width;
  }

  let result = '';
  for (let start = 0; start < codePoints.length; start += 4096) {
    result += String.fromCodePoint(...codePoints.slice(start, start + 4096));
  }
  return result;
}

/**
 * 严格 UTF-16 解码：拒绝奇数长度、孤立代理项。
 */
export function decodeUtf16(bytes: Uint8Array, littleEndian: boolean): string {
  if (bytes.length % 2 !== 0) {
    throw new XmlSecurityError('ENCODING_MALFORMED', 'UTF-16 input has an odd number of bytes');
  }
  const codeUnits: number[] = [];
  for (let i = 0; i < bytes.length; i += 2) {
    const high = bytes[i] as number;
    const low = bytes[i + 1] as number;
    codeUnits.push(littleEndian ? (low << 8) | high : (high << 8) | low);
  }
  for (let i = 0; i < codeUnits.length; i++) {
    const unit = codeUnits[i] as number;
    if (unit >= 0xd800 && unit <= 0xdbff) {
      const next = codeUnits[i + 1];
      if (next === undefined || next < 0xdc00 || next > 0xdfff) {
        throw new XmlSecurityError('ENCODING_MALFORMED', 'unpaired UTF-16 high surrogate', i * 2);
      }
      i++;
    } else if (unit >= 0xdc00 && unit <= 0xdfff) {
      throw new XmlSecurityError('ENCODING_MALFORMED', 'unpaired UTF-16 low surrogate', i * 2);
    }
  }
  let result = '';
  for (let start = 0; start < codeUnits.length; start += 4096) {
    result += String.fromCharCode(...codeUnits.slice(start, start + 4096));
  }
  return result;
}

/**
 * 原始字节入口：BOM 识别 + 字节序嗅探 + 声明一致性审计 + 严格解码。
 * 仅放行 UTF-8 / UTF-16LE / UTF-16BE 白名单编码。
 */
export function decodeXmlInput(input: Uint8Array): DecodeResult {
  if (!(input instanceof Uint8Array)) {
    throw new XmlSecurityError('ENCODING_MALFORMED', 'input must be a Uint8Array of raw XML bytes');
  }
  if (input.length === 0) {
    throw new XmlSecurityError('ENCODING_MALFORMED', 'empty XML input');
  }

  let encoding: WhitelistedEncoding;
  let payload: Uint8Array;
  let hadBom = false;

  if (hasPrefix(input, UTF16LE_BOM)) {
    encoding = 'UTF-16LE';
    payload = input.subarray(2);
    hadBom = true;
  } else if (hasPrefix(input, UTF16BE_BOM)) {
    encoding = 'UTF-16BE';
    payload = input.subarray(2);
    hadBom = true;
  } else if (hasPrefix(input, UTF8_BOM)) {
    encoding = 'UTF-8';
    payload = input.subarray(3);
    hadBom = true;
  } else {
    encoding = sniffEncoding(input);
    payload = input;
  }

  const text =
    encoding === 'UTF-8'
      ? decodeUtf8(payload)
      : decodeUtf16(payload, encoding === 'UTF-16LE');

  const declared = readXmlDeclarationEncoding(text);
  if (declared !== undefined) {
    const canonical = ENCODING_ALIASES[declared];
    if (canonical === undefined) {
      throw new XmlSecurityError(
        'ENCODING_NOT_WHITELISTED',
        `declared encoding "${declared}" is not on the UTF-8/UTF-16 whitelist`,
      );
    }
    const compatible =
      (encoding === 'UTF-16LE' && canonical === 'UTF-16LE') ||
      (encoding === 'UTF-16BE' && canonical === 'UTF-16BE') ||
      (encoding === 'UTF-8' && canonical === 'UTF-8');
    if (!compatible) {
      throw new XmlSecurityError(
        'ENCODING_MISMATCH',
        `declared encoding "${declared}" does not match detected ${encoding}`,
      );
    }
  }

  return { text, encoding, hadBom, declaredEncoding: declared };
}
