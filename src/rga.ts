/**
 * RGA（Replicated Growable Array）字符序列。
 * 删除是墓碑（visible=false），恢复是清除墓碑，二者幂等，天然适合补偿式撤销。
 */
import type { CharId, SiteId } from './types.ts';

export interface Char {
  id: CharId;
  value: string;
  visible: boolean;
}

/** 比较字符 id：先比 counter，再比 site，保证全序 */
export function compareCharId(a: CharId, b: CharId): number {
  const [siteA, counterA] = a.split(':');
  const [siteB, counterB] = b.split(':');
  const diff = Number(counterA) - Number(counterB);
  if (diff !== 0) return diff;
  return siteA < siteB ? -1 : siteA > siteB ? 1 : 0;
}

export function makeCharId(site: SiteId, counter: number): CharId {
  return `${site}:${counter}`;
}

export class RgaDoc {
  /** 完整字符序列（含墓碑），顺序即文档顺序 */
  private chars: Char[] = [];
  private index = new Map<CharId, number>();

  get size(): number {
    return this.chars.length;
  }

  has(id: CharId): boolean {
    return this.index.has(id);
  }

  /** 可见文本（墓碑不显示） */
  text(): string {
    let out = '';
    for (const c of this.chars) if (c.visible) out += c.value;
    return out;
  }

  /** 当前可见文本末尾字符的 id（供追加插入定位），空文档返回 null */
  lastVisibleCharId(): CharId | null {
    for (let i = this.chars.length - 1; i >= 0; i--) {
      if (this.chars[i].visible) return this.chars[i].id;
    }
    return null;
  }

  /** 在 after 之后插入一个字符（RGA 集成算法，幂等） */
  integrateInsert(id: CharId, value: string, after: CharId | null): void {
    if (this.index.has(id)) return; // 幂等
    let i = after === null ? 0 : this.index.get(after)! + 1;
    // 跳过所有 id 更大的并发后继，保证各副本收敛到同一顺序
    while (i < this.chars.length && compareCharId(this.chars[i].id, id) > 0) i++;
    this.chars.splice(i, 0, { id, value, visible: true });
    for (let j = i; j < this.chars.length; j++) this.index.set(this.chars[j].id, j);
  }

  /** 墓碑删除（幂等） */
  applyDelete(id: CharId): void {
    const c = this.chars[this.index.get(id) ?? -1];
    if (c) c.visible = false;
  }

  /** 恢复（撤销删除 / 重做插入，幂等） */
  applyRestore(id: CharId): void {
    const c = this.chars[this.index.get(id) ?? -1];
    if (c) c.visible = true;
  }
}
