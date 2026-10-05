import type { ResolvedReference } from './types.ts';

export function formatReferenceDisplay(resolved: ResolvedReference): string {
  if (resolved.status === 'dangling') {
    if (resolved.lastKnown) {
      const { version, number, title } = resolved.lastKnown;
      return `§${number ?? '?'} ${title}（引用已失效，最后见于 v${version}）`;
    }
    return `§? ${resolved.targetId}（引用已失效）`;
  }
  const current = `§${resolved.number ?? '?'} ${resolved.title}`;
  if (resolved.pinned && resolved.changedSincePinned) {
    const pinned = `§${resolved.pinned.number ?? '?'} ${resolved.pinned.title}`;
    return `${pinned}（v${resolved.pinned.version}，当前为 ${current}）`;
  }
  return current;
}
