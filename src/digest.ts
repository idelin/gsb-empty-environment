import { createHash } from 'crypto';
import type { ArtifactManifest } from './types';

export function sha256Hex(data: Buffer | string): string {
  return createHash('sha256').update(data).digest('hex');
}

/** 清单内容指纹：用于识别重复清单（幂等）与防止清单被中途替换。 */
export function manifestDigest(manifest: ArtifactManifest): string {
  const canonical = {
    taskId: manifest.taskId,
    inputDigest: manifest.inputDigest,
    toolVersion: manifest.toolVersion,
    attempt: manifest.attempt,
    files: manifest.files.map((f) => ({
      path: f.path,
      size: f.size,
      sha256: f.sha256,
      chunks: f.chunks.map((c) => ({ index: c.index, size: c.size, sha256: c.sha256 })),
    })),
  };
  return sha256Hex(JSON.stringify(canonical));
}
