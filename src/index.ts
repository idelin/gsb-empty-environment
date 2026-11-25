import { BinaryStreamDecoder } from './binary.ts';
import { JsonStreamDecoder } from './json.ts';
import { compileGraph, type CompiledGraph } from './schema.ts';
import type { DecodeOptions, DecodeResult, TypeGraph } from './types.ts';

export * from './types.ts';
export { compileGraph, type CompiledGraph } from './schema.ts';
export { JsonStreamDecoder, type JsonCheckpoint } from './json.ts';
export { BinaryStreamDecoder, type BinaryCheckpoint } from './binary.ts';
export { decodeJsonReference, decodeBinaryReference } from './reference.ts';
export { encodeRootMessage } from './encode-binary.ts';
export { canonicalId64FromText, canonicalId64FromNumberLexeme, id64FromBigInt } from './lex64.ts';
export { encodeMinimal, buildSignatureKey, type IdNode } from './identity.ts';

export type AuthorizeCallback = (result: DecodeResult) => void;

/**
 * 整量 JSON 解码。授权回调只在全部输入通过校验后、结果生成之后触发；
 * 任何校验失败都会抛出 DecodeError，回调不会执行。
 */
export function decodeJson(
  graph: CompiledGraph | TypeGraph,
  jsonText: string,
  options: DecodeOptions,
  onAuthorize?: AuthorizeCallback,
): DecodeResult {
  const compiled = compileIfNeeded(graph);
  const decoder = new JsonStreamDecoder(compiled, options);
  decoder.push(new TextEncoder().encode(jsonText));
  const result = decoder.end();
  onAuthorize?.(result);
  return result;
}

/** 整量二进制解码，回调时机与 JSON 入口完全一致。 */
export function decodeBinary(
  graph: CompiledGraph | TypeGraph,
  bytes: Uint8Array,
  options: DecodeOptions,
  onAuthorize?: AuthorizeCallback,
): DecodeResult {
  const compiled = compileIfNeeded(graph);
  const decoder = new BinaryStreamDecoder(compiled, options);
  decoder.push(bytes);
  const result = decoder.end();
  onAuthorize?.(result);
  return result;
}

function compileIfNeeded(graph: CompiledGraph | TypeGraph): CompiledGraph {
  if ('digest' in graph && 'types' in graph && graph.types instanceof Map) {
    return graph as CompiledGraph;
  }
  return compileGraph(graph as TypeGraph);
}
