/**
 * 字典：维护 字符串 <-> 编码id 的双向映射。
 * 写时追加（append-only）：新词汇流入时分配单调递增的 id，永不复用、永不删除。
 */
export class Dictionary {
  private readonly valueToId = new Map<string, number>();
  private readonly idToValue: string[] = [];

  /** 当前字典基数（不同词汇的数量）。 */
  get size(): number {
    return this.idToValue.length;
  }

  /** 查询词汇的编码 id；不存在返回 -1，不产生副作用。 */
  lookup(value: string): number {
    const id = this.valueToId.get(value);
    return id === undefined ? -1 : id;
  }

  /**
   * 写入词汇并返回其编码 id。
   * 已存在则直接返回原 id；新词汇追加到字典尾部并分配新 id。
   */
  intern(value: string): { id: number; isNew: boolean } {
    const existing = this.valueToId.get(value);
    if (existing !== undefined) {
      return { id: existing, isNew: false };
    }
    const id = this.idToValue.length;
    this.idToValue.push(value);
    this.valueToId.set(value, id);
    return { id, isNew: true };
  }

  /** 按编码 id 取回原始字符串。 */
  value(id: number): string {
    if (!Number.isInteger(id) || id < 0 || id >= this.idToValue.length) {
      throw new RangeError(`dictionary id out of range: ${id}`);
    }
    return this.idToValue[id];
  }
}
