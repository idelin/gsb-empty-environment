import { Dictionary } from "./dictionary.ts";

/** 编码位向量的存储位宽。 */
export type StorageWidth = 8 | 16 | 32;

type CodeVector = Uint8Array | Uint16Array | Uint32Array;

/** 一次热重构（存储层升级）事件的快照。 */
export interface UpgradeEvent {
  fromWidth: StorageWidth;
  toWidth: StorageWidth;
  /** 触发升级时的字典基数。 */
  dictionarySize: number;
  /** 触发升级时列中已有的行数。 */
  rowCount: number;
}

/** 各档存储位宽能容纳的最大编码 id。 */
const MAX_CODE: Record<StorageWidth, number> = {
  8: 0xff, // 255
  16: 0xffff, // 65535
  32: 0xffffffff, // 4294967295
};

const WIDTH_ORDER: readonly StorageWidth[] = [8, 16, 32];

function createCodeVector(width: StorageWidth, capacity: number): CodeVector {
  switch (width) {
    case 8:
      return new Uint8Array(capacity);
    case 16:
      return new Uint16Array(capacity);
    case 32:
      return new Uint32Array(capacity);
  }
}

/**
 * 字典编码列存 + 热重构器。
 *
 * - 写时追加：append() 将字符串编码为整数 id 存入位向量；
 * - 动态扩容：字典随新词汇单调增长，位向量容量按需倍增；
 * - 热重构：当新词汇的编码 id 超出当前位宽上限（如 uint8 的 255）时，
 *   自动将底层位向量重写为下一档位宽（uint8 -> uint16 -> uint32），
 *   重写采用“构建新向量 -> 拷贝 -> 原子换引用”的方式，读写服务不中断。
 */
export class DictionaryEncodedColumn {
  private readonly dictionary = new Dictionary();
  private codes: CodeVector;
  private width: StorageWidth = 8;
  private rowCount = 0;
  private readonly upgradeLog: UpgradeEvent[] = [];
  private readonly listeners: Array<(event: UpgradeEvent) => void> = [];

  constructor(initialRowCapacity = 16) {
    this.codes = createCodeVector(this.width, Math.max(1, initialRowCapacity));
  }

  /** 已写入的行数。 */
  get length(): number {
    return this.rowCount;
  }

  /** 字典基数。 */
  get dictionarySize(): number {
    return this.dictionary.size;
  }

  /** 当前位向量存储位宽。 */
  get storageWidth(): StorageWidth {
    return this.width;
  }

  /** 历次热重构事件日志（只读副本）。 */
  get upgrades(): readonly UpgradeEvent[] {
    return this.upgradeLog.slice();
  }

  /** 订阅存储层升级事件（用于监控/告警）。 */
  onUpgrade(listener: (event: UpgradeEvent) => void): void {
    this.listeners.push(listener);
  }

  /**
   * 追加一行。返回行号。
   * 若新词汇导致编码 id 突破当前位宽门限，先同步完成热重构再写入，
   * 对调用方而言只是一次普通的 append。
   */
  append(value: string): number {
    const { id } = this.dictionary.intern(value);
    this.ensureWidthFor(id);
    this.ensureRowCapacity(this.rowCount + 1);
    this.codes[this.rowCount] = id;
    return this.rowCount++;
  }

  /** 按行号读取原始字符串。 */
  get(rowIndex: number): string {
    if (!Number.isInteger(rowIndex) || rowIndex < 0 || rowIndex >= this.rowCount) {
      throw new RangeError(`row index out of range: ${rowIndex}`);
    }
    return this.dictionary.value(this.codes[rowIndex]);
  }

  /** 反向查询：字符串 -> 编码 id（不存在返回 -1）。 */
  codeOf(value: string): number {
    return this.dictionary.lookup(value);
  }

  /** 列统计信息。 */
  stats(): {
    rowCount: number;
    dictionarySize: number;
    storageWidth: StorageWidth;
    upgradeCount: number;
    memoryBytes: number;
  } {
    return {
      rowCount: this.rowCount,
      dictionarySize: this.dictionary.size,
      storageWidth: this.width,
      upgradeCount: this.upgradeLog.length,
      memoryBytes: this.codes.byteLength,
    };
  }

  /** 若 code 超出当前位宽上限，逐级升级存储层并重写位向量。 */
  private ensureWidthFor(code: number): void {
    while (code > MAX_CODE[this.width]) {
      const nextWidth = WIDTH_ORDER[WIDTH_ORDER.indexOf(this.width) + 1];
      if (nextWidth === undefined) {
        throw new RangeError(`code ${code} exceeds uint32 dictionary capacity`);
      }
      this.reencode(nextWidth);
    }
  }

  /**
   * 热重构核心：将位向量原地“热”迁移到更宽的存储。
   * 先完整构建新向量并拷贝全部既有编码，再一次引用交换完成切换；
   * 切换前旧向量保持可读，切换后新向量数据完整，查询全程正确。
   */
  private reencode(targetWidth: StorageWidth): void {
    const next = createCodeVector(targetWidth, this.codes.length);
    next.set(this.codes);
    const event: UpgradeEvent = {
      fromWidth: this.width,
      toWidth: targetWidth,
      dictionarySize: this.dictionary.size,
      rowCount: this.rowCount,
    };
    this.codes = next;
    this.width = targetWidth;
    this.upgradeLog.push(event);
    for (const listener of this.listeners) {
      listener(event);
    }
  }

  /** 行容量不足时按倍增策略扩容（不改变位宽）。 */
  private ensureRowCapacity(required: number): void {
    if (required <= this.codes.length) {
      return;
    }
    let capacity = this.codes.length;
    while (capacity < required) {
      capacity *= 2;
    }
    const next = createCodeVector(this.width, capacity);
    next.set(this.codes.subarray(0, this.rowCount));
    this.codes = next;
  }
}
