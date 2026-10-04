import { Entry, Key } from './types';

/**
 * 乙：迁入方分片。
 * 应用规则：仅当条目版本（seq）更新时才覆盖 —— 乱序/重复的增量因此幂等；
 * 删除以墓碑形式保留，防止旧版本的 put 在复制时把记录“复活”。
 */
export class TargetNode {
  private data = new Map<Key, Entry>();

  /** @returns true 表示条目被接受（版本更新），false 表示过期/重复被丢弃 */
  apply(key: Key, entry: Entry): boolean {
    const cur = this.data.get(key);
    if (cur && cur.seq >= entry.seq) {
      return false;
    }
    this.data.set(key, { seq: entry.seq, value: entry.value });
    return true;
  }

  /** 读路径：墓碑与不存在一样不可见 */
  visibleGet(key: Key): string | undefined {
    const entry = this.data.get(key);
    return entry && entry.value !== null ? entry.value : undefined;
  }

  rawEntry(key: Key): Entry | undefined {
    const entry = this.data.get(key);
    return entry ? { ...entry } : undefined;
  }

  keys(): Key[] {
    return [...this.data.keys()];
  }
}
