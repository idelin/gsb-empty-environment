import { VfsError, splitPath } from '../src/index.ts';
import type { VfsStore } from '../src/index.ts';

/** Creates intermediate nodes as needed; returns the id of the final node. */
export function mkpath(store: VfsStore, path: string): string {
  let cur = store.tree.rootId;
  for (const seg of splitPath(path)) {
    const next = store.tree.childByName(cur, seg);
    cur = next ?? store.tree.createNode(cur, seg);
  }
  return cur;
}

export function hasCode(code: string): (err: unknown) => boolean {
  return (err) => err instanceof VfsError && err.code === code;
}
