import { sha256Hex } from './digest';
import type { ArtifactManifest, ExpectedContext, FileSpec } from './types';

export const CHUNK_SIZE = 4;

/** 由真实内容构建文件描述（分块 + 校验和）。 */
export function fileSpecFromContent(path: string, content: string, chunkSize = CHUNK_SIZE): FileSpec {
  const buf = Buffer.from(content, 'utf8');
  const chunks = [];
  for (let offset = 0, index = 0; offset < buf.length; offset += chunkSize, index += 1) {
    const part = buf.subarray(offset, Math.min(offset + chunkSize, buf.length));
    chunks.push({ index, size: part.length, sha256: sha256Hex(part) });
  }
  return { path, size: buf.length, sha256: sha256Hex(buf), chunks };
}

export function makeContext(attempt: number, overrides: Partial<ExpectedContext> = {}): ExpectedContext {
  return {
    taskId: 'task-001',
    inputDigest: sha256Hex('input-payload'),
    toolVersion: 'worker/2.3.1',
    attempt,
    ...overrides,
  };
}

export function makeManifest(
  ctx: ExpectedContext,
  files: FileSpec[],
  overrides: Partial<ArtifactManifest> = {},
): ArtifactManifest {
  return {
    taskId: ctx.taskId,
    inputDigest: ctx.inputDigest,
    toolVersion: ctx.toolVersion,
    attempt: ctx.attempt,
    files,
    ...overrides,
  };
}

/** 按清单把内容切成分块，模拟执行器侧的真实数据。 */
export function chunkData(contents: ReadonlyMap<string, string>, path: string, index: number): Buffer {
  const content = contents.get(path);
  if (content === undefined) throw new Error(`no content for ${path}`);
  const buf = Buffer.from(content, 'utf8');
  return buf.subarray(index * CHUNK_SIZE, (index + 1) * CHUNK_SIZE);
}
