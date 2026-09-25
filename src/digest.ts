import { createHash } from 'node:crypto';
import { ChunkRef, JsonDagError, isObject } from './json.ts';
import type { ChunkReader, Json, Path } from './json.ts';

export type HashFn = (data: string) => string;

export const sha256: HashFn = (data) => createHash('sha256').update(data).digest('hex');

export type ArrayMode = 'position' | 'id';

export interface DigestOptions {
  /** Content hash. Injectable so tests can force collisions. Defaults to SHA-256. */
  hash?: HashFn;
  /** Per-path array mode: stable-id keyed or explicit position. Defaults to 'position'. */
  arrayMode?: (path: Path) => ArrayMode;
  /** Key holding the stable element id when arrayMode is 'id'. Defaults to 'id'. */
  idKey?: string;
}

export type NodeKind = 'primitive' | 'array' | 'object';

/** A node of the content digest DAG. Children are keyed by object key,
 *  `#<index>` (position arrays) or `id:<id>` (stable-id arrays). */
export interface DigestNode {
  digest: string;
  kind: NodeKind;
  children: Map<string, DigestNode>;
}

export function elementKey(el: Json, index: number, mode: ArrayMode, idKey: string, path: Path): string {
  if (mode === 'position') return `#${index}`;
  const id = isObject(el) ? el[idKey] : undefined;
  if (typeof id !== 'string' && typeof id !== 'number') {
    throw new JsonDagError(`array element is missing stable id key "${idKey}"`, [...path, index]);
  }
  return `id:${String(id)}`;
}

export function buildDigest(
  node: Json | ChunkRef,
  opts: DigestOptions = {},
  reader?: ChunkReader,
  path: Path = [],
): DigestNode {
  const hash = opts.hash ?? sha256;
  if (node instanceof ChunkRef) {
    if (!reader) throw new JsonDagError('a chunk reader is required to resolve chunk references', path);
    return reader.nodeOf(node.id);
  }
  if (Array.isArray(node)) {
    const mode = opts.arrayMode?.(path) ?? 'position';
    const idKey = opts.idKey ?? 'id';
    const children = new Map<string, DigestNode>();
    const seen = new Set<string>();
    const parts: string[] = [];
    node.forEach((el, i) => {
      const key = elementKey(el, i, mode, idKey, path);
      if (mode === 'id') {
        if (seen.has(key)) {
          throw new JsonDagError(`duplicate array element id ${JSON.stringify(key.slice(3))}`, [...path, i]);
        }
        seen.add(key);
      }
      const child = buildDigest(el, opts, reader, [...path, i]);
      children.set(key, child);
      parts.push(`${key}=${child.digest}`);
    });
    return { digest: hash(`A[${parts.join(',')}]`), kind: 'array', children };
  }
  if (isObject(node)) {
    const children = new Map<string, DigestNode>();
    const parts: string[] = [];
    for (const key of Object.keys(node).sort()) {
      const child = buildDigest(node[key], opts, reader, [...path, key]);
      children.set(key, child);
      parts.push(`${JSON.stringify(key)}=${child.digest}`);
    }
    return { digest: hash(`O{${parts.join(',')}}`), kind: 'object', children };
  }
  return { digest: hash(`V${JSON.stringify(node)}`), kind: 'primitive', children: new Map() };
}

export function digestOfTree(value: Json | ChunkRef, opts: DigestOptions = {}, reader?: ChunkReader): string {
  return buildDigest(value, opts, reader).digest;
}
