import { BinaryStreamDecoder } from './binary.ts';
import { JsonStreamDecoder } from './json.ts';
import type { CompiledGraph } from './schema.ts';
import type { DecodeOptions, DecodeResult } from './types.ts';

/**
 * 小对象参考路径：一次性喂入完整消息。
 *
 * 该实现与流式入口共享同一套 tokenizer/状态机和 schema 校验，
 * 但语义上作为“独立参考”：测试用它比较两个入口在所有字节切点下
 * 是否产生完全相同的 minimal 编码与 signatureKey。
 */

const textEncoder = new TextEncoder();

export function decodeJsonReference(
  graph: CompiledGraph,
  jsonText: string,
  options: DecodeOptions,
): DecodeResult {
  const decoder = new JsonStreamDecoder(graph, options);
  decoder.push(textEncoder.encode(jsonText));
  return decoder.end();
}

export function decodeBinaryReference(
  graph: CompiledGraph,
  bytes: Uint8Array,
  options: DecodeOptions,
): DecodeResult {
  const decoder = new BinaryStreamDecoder(graph, options);
  decoder.push(bytes);
  return decoder.end();
}
