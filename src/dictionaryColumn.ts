/**
 * DictionaryEncodedColumn
 * -----------------------
 * 列存字典编码管理器：
 *  - 写时追加（append-only）地将任意字符串编码为整数码字存入底层列；
 *  - 新词汇流入时动态扩展字典（string -> code 双向映射）；
 *  - 当字典基数突破当前存储位宽上限时，自动触发“热重构”：
 *    底层码字数组从 uint8 平滑迁移到 uint16（可继续扩展到 32 位），
 *    迁移过程就地重写位向量，对外的 append/get 语义不中断、结果一致。
 */

export type CodeWidth = 8 | 16 | 32;
export type CodeBuffer = Uint8Array | Uint16Array | Uint32Array;

export interface MigrationEvent {
  fromWidth: CodeWidth;
  toWidth: CodeWidth;
  /** 迁移发生时被重写的行数 */
  rowsRewritten: number;
  /** 触发迁移时的字典基数 */
  dictionarySize: number;
  /** 触发迁移的新词汇 */
  triggerValue: string;
  timestamp: number;
}

export interface ColumnStats {
  rows: number;
  dictionarySize: number;
  widthBits: CodeWidth;
  capacity: number;
  migrationCount: number;
}

const WIDTH_MAX_CODE: Record<CodeWidth, number> = {
  8: 0xff,        // 255
  16: 0xffff,     // 65535
  32: 0xffffffff, // 4294967295
};

const WIDTH_CTOR: Record<CodeWidth, new (length: number) => CodeBuffer> = {
  8: Uint8Array,
  16: Uint16Array,
  32: Uint32Array,
};

const NEXT_WIDTH: Record<CodeWidth, CodeWidth | null> = {
  8: 16,
  16: 32,
  32: null,
};

export class DictionaryEncodedColumn {
  private dictionary: string[] = [];
  private codeByValue = new Map<string, number>();
  private codes: CodeBuffer;
  private width: CodeWidth = 8;
  private rowCount = 0;
  private migrations: MigrationEvent[] = [];
  private listeners: Array<(e: MigrationEvent) => void> = [];

  constructor(initialCapacity = 16) {
    if (initialCapacity < 1) throw new RangeError("initialCapacity must be >= 1");
    this.codes = new Uint8Array(initialCapacity);
  }

  /** 订阅热重构事件（用于观测/审计，不影响写入路径） */
  onMigration(listener: (e: MigrationEvent) => void): void {
    this.listeners.push(listener);
  }

  /** 追加一个值；必要时先扩容字典、触发热迁移。返回该行的码字。 */
  append(value: string): number {
    let code = this.codeByValue.get(value);
    if (code === undefined) {
      code = this.dictionary.length;
      this.ensureWidthFor(code, value);
      this.dictionary.push(value);
      this.codeByValue.set(value, code);
    }
    this.ensureCapacityFor(this.rowCount + 1);
    this.codes[this.rowCount] = code;
    this.rowCount += 1;
    return code;
  }

  /** 按行号读取原始值。 */
  get(rowIndex: number): string {
    if (rowIndex < 0 || rowIndex >= this.rowCount) {
      throw new RangeError(`row index ${rowIndex} out of bounds (rows=${this.rowCount})`);
    }
    return this.dictionary[this.codes[rowIndex]];
  }

  /** 按行号读取码字（调试用）。 */
  codeAt(rowIndex: number): number {
    if (rowIndex < 0 || rowIndex >= this.rowCount) {
      throw new RangeError(`row index ${rowIndex} out of bounds (rows=${this.rowCount})`);
    }
    return this.codes[rowIndex];
  }

  /** 顺序扫描全部行。 */
  *scan(): IterableIterator<string> {
    for (let i = 0; i < this.rowCount; i++) {
      yield this.dictionary[this.codes[i]];
    }
  }

  get stats(): ColumnStats {
    return {
      rows: this.rowCount,
      dictionarySize: this.dictionary.length,
      widthBits: this.width,
      capacity: this.codes.length,
      migrationCount: this.migrations.length,
    };
  }

  get migrationHistory(): readonly MigrationEvent[] {
    return this.migrations;
  }

  /**
   * 热重构核心：当新码字超出当前位宽时，把底层码字数组整体升级一档，
   * 并逐行重写已有位向量（copy 语义）。迁移期间不加锁——JS 单线程模型下
   * 本方法是同步原子的，任何在迁移前后发起的 get/append 都看到一致状态。
   */
  private ensureWidthFor(nextCode: number, triggerValue: string): void {
    if (nextCode <= WIDTH_MAX_CODE[this.width]) return;
    const fromWidth = this.width;
    const toWidth = NEXT_WIDTH[fromWidth];
    if (toWidth === null || nextCode > WIDTH_MAX_CODE[toWidth]) {
      throw new RangeError(`dictionary overflow: code ${nextCode} exceeds uint32 range`);
    }
    const upgraded = new WIDTH_CTOR[toWidth](this.codes.length);
    upgraded.set(this.codes); // 逐行重写位向量：uint8 -> uint16
    this.codes = upgraded;
    this.width = toWidth;
    const event: MigrationEvent = {
      fromWidth,
      toWidth,
      rowsRewritten: this.rowCount,
      dictionarySize: this.dictionary.length + 1,
      triggerValue,
      timestamp: Date.now(),
    };
    this.migrations.push(event);
    for (const listener of this.listeners) listener(event);
  }

  /** 行数容量扩容（与位宽迁移正交）：倍增策略。 */
  private ensureCapacityFor(requiredRows: number): void {
    if (requiredRows <= this.codes.length) return;
    let next = this.codes.length;
    while (next < requiredRows) next *= 2;
    const grown = new WIDTH_CTOR[this.width](next);
    grown.set(this.codes);
    this.codes = grown;
  }
}
