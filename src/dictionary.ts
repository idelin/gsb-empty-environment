import { canonicalJson, deepFreeze, fingerprint } from './fingerprint.ts';
import type { CompressionDictionary, EncodedReport, WireReport } from './types.ts';

type EncodedNode =
  | null
  | boolean
  | number
  | string
  | EncodedNode[]
  | { $s: string }
  | { $d: number }
  | { $o: ReadonlyArray<readonly [EncodedNode, EncodedNode]> };

export function createCompressionDictionary(
  id: string,
  tokens: readonly string[],
): CompressionDictionary {
  const uniqueTokens = [...new Set(tokens)].sort();
  const withoutHash = {
    kind: 'gsb.compression-dictionary/v1' as const,
    id,
    scope: 'public-shared' as const,
    tokens: uniqueTokens,
  };
  const hash = fingerprint(withoutHash);
  return deepFreeze({ ...withoutHash, hash });
}

export function encodeReportWithDictionary(
  report: WireReport,
  dictionary: CompressionDictionary,
): EncodedReport {
  const tokenIndex = new Map(dictionary.tokens.map((token, index) => [token, index]));
  const value = encodeNode(report, tokenIndex);
  const payload = canonicalJson(value);
  return deepFreeze({
    algorithm: 'gsb.dict-ref/v1' as const,
    dictionaryId: dictionary.id,
    dictionaryHash: dictionary.hash,
    value,
    payload,
  });
}

export function decodeReportWithDictionary(
  encoded: EncodedReport,
  dictionary: CompressionDictionary,
): WireReport {
  if (encoded.dictionaryHash !== dictionary.hash) {
    throw new Error('dictionary hash mismatch');
  }
  return decodeNode(encoded.value, dictionary.tokens) as WireReport;
}

function encodeNode(
  value: unknown,
  tokenIndex: ReadonlyMap<string, number>,
): EncodedNode {
  if (value === null || typeof value === 'boolean' || typeof value === 'number') {
    return value;
  }
  if (typeof value === 'string') {
    const index = tokenIndex.get(value);
    return index === undefined ? { $s: value } : { $d: index };
  }
  if (Array.isArray(value)) {
    return value.map((item) => encodeNode(item, tokenIndex));
  }
  if (typeof value === 'object') {
    const record = value as Record<string, unknown>;
    const entries = Object.keys(record)
      .sort()
      .map((key) => [encodeNode(key, tokenIndex), encodeNode(record[key], tokenIndex)] as const);
    return { $o: entries };
  }
  throw new TypeError(`Cannot encode ${typeof value}`);
}

function decodeNode(node: EncodedNode, tokens: readonly string[]): unknown {
  if (node === null || typeof node === 'boolean' || typeof node === 'number') {
    return node;
  }
  if (typeof node === 'string') {
    return node;
  }
  if (Array.isArray(node)) {
    return node.map((item) => decodeNode(item, tokens));
  }
  if ('$s' in node) {
    return node.$s;
  }
  if ('$d' in node) {
    const token = tokens[node.$d];
    if (token === undefined) {
      throw new RangeError(`Missing dictionary token ${node.$d}`);
    }
    return token;
  }
  const result: Record<string, unknown> = {};
  for (const [keyNode, valueNode] of node.$o) {
    const key = decodeNode(keyNode, tokens);
    if (typeof key !== 'string') {
      throw new TypeError('Encoded object key did not decode to a string');
    }
    result[key] = decodeNode(valueNode, tokens);
  }
  return result;
}
