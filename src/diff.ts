import { normalizeSnapshot } from './snapshot.ts';
import type { NodeKey, NormalizedNode, SnapshotIndex, SnapshotNode } from './types.ts';

export interface RenameEntry {
  key: NodeKey;
  node: NormalizedNode;
  fromName: string;
}

export interface MoveEntry {
  key: NodeKey;
  node: NormalizedNode;
  fromParentKey: NodeKey | null;
  toParentKey: NodeKey | null;
}

export interface ContentEntry {
  key: NodeKey;
  node: NormalizedNode;
  fromDigest: string | null;
}

/** 快照差异结果。同一 id 的搬移/改名/改内容都归到对应类别，绝不会报成“删除+新增”。 */
export interface SnapshotDiff {
  added: NormalizedNode[];
  removed: NormalizedNode[];
  renamed: RenameEntry[];
  moved: MoveEntry[];
  contentChanged: ContentEntry[];
}

export function diffSnapshots(source: SnapshotNode[], target: SnapshotNode[]): SnapshotDiff {
  return diffIndexes(normalizeSnapshot(source, 'source'), normalizeSnapshot(target, 'target'));
}

/**
 * 以 (id, generation) 为身份做匹配：
 * - 键相同 → 比较 name / parent / digest，得出改名、移动、内容变化；
 * - 键只在单侧出现 → 新增或删除（id 复用但代际不同自然落在这里，按代际区分）；
 * - 键相同但类型不同（文件变目录等）→ 视为删除 + 新增。
 */
export function diffIndexes(source: SnapshotIndex, target: SnapshotIndex): SnapshotDiff {
  const diff: SnapshotDiff = {
    added: [],
    removed: [],
    renamed: [],
    moved: [],
    contentChanged: [],
  };

  for (const [key, s] of source.nodes) {
    const t = target.nodes.get(key);
    if (!t) {
      diff.removed.push(s);
      continue;
    }
    if (s.type !== t.type) {
      diff.removed.push(s);
      diff.added.push(t);
      continue;
    }
    if (s.name !== t.name) {
      diff.renamed.push({ key, node: t, fromName: s.name });
    }
    const fromParentKey = source.parentKeyOf.get(key) ?? null;
    const toParentKey = target.parentKeyOf.get(key) ?? null;
    if (fromParentKey !== toParentKey) {
      diff.moved.push({ key, node: t, fromParentKey, toParentKey });
    }
    if (t.type === 'file' && s.digest !== t.digest) {
      diff.contentChanged.push({ key, node: t, fromDigest: s.digest });
    }
  }

  for (const [key, t] of target.nodes) {
    if (!source.nodes.has(key)) {
      if (!diff.added.includes(t)) diff.added.push(t);
    }
  }

  return diff;
}
