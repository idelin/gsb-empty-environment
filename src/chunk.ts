import { ChunkRef, JsonDagError } from './json.ts';
import type { ChunkReader, Json, Path } from './json.ts';
import { buildDigest } from './digest.ts';
import type { DigestNode, DigestOptions } from './digest.ts';
import { isObject } from './json.ts';

/** Content-addressed in-memory chunk store. `reads` counts actual content loads
 *  (`get`); digest lookups (`nodeOf`/`digestOf`) never read content, which is what
 *  makes digest pruning possible for large trees. */
export class MemChunkStore implements ChunkReader {
  reads = 0;
  private chunks = new Map<string, Json>();
  private nodes = new Map<string, DigestNode>();
  private opts: DigestOptions;

  constructor(opts: DigestOptions = {}) {
    this.opts = opts;
  }

  put(subtree: Json): ChunkRef {
    const node = buildDigest(subtree, this.opts, this);
    if (!this.chunks.has(node.digest)) {
      this.chunks.set(node.digest, subtree);
      this.nodes.set(node.digest, node);
    }
    return new ChunkRef(node.digest);
  }

  get(id: string): Json {
    this.reads++;
    const chunk = this.chunks.get(id);
    if (chunk === undefined) throw new JsonDagError(`missing chunk ${id}`, []);
    return chunk;
  }

  nodeOf(id: string): DigestNode {
    const node = this.nodes.get(id);
    if (!node) throw new JsonDagError(`missing chunk digest ${id}`, []);
    return node;
  }

  digestOf(id: string): string {
    return this.nodeOf(id).digest;
  }

  get size(): number {
    return this.chunks.size;
  }
}

/** Replaces subtrees selected by `shouldChunk` with ChunkRefs, bottom-up, so
 *  chunks may themselves contain references to deeper chunks. */
export function chunkify(
  value: Json,
  store: MemChunkStore,
  shouldChunk: (path: Path, subtree: Json) => boolean,
  path: Path = [],
): Json {
  let v = value;
  if (Array.isArray(v)) v = v.map((el, i) => chunkify(el, store, shouldChunk, [...path, i])) as Json;
  else if (isObject(v)) {
    const out: Record<string, Json> = {};
    for (const k of Object.keys(v)) out[k] = chunkify(v[k], store, shouldChunk, [...path, k]);
    v = out;
  }
  if (shouldChunk(path, v)) return store.put(v) as unknown as Json;
  return v;
}
