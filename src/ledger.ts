/**
 * 命中账本：缓存每一次关键决策都追加一条不可变事件，
 * 用于事后核对「为什么命中 / 为什么没命中 / 结果去了哪里」。
 */
export const LEDGER_KINDS = [
  'miss',
  'start',
  'coalesced',
  'hit',
  'store',
  'abort',
  'discard-stale',
  'discard-cancelled',
  'evict',
  'error',
] as const;

export type LedgerKind = (typeof LEDGER_KINDS)[number];

export interface LedgerEvent {
  readonly seq: number;
  readonly kind: LedgerKind;
  readonly key: string;
  readonly note: string;
}

export class Ledger {
  private readonly events: LedgerEvent[] = [];
  private seq = 0;

  record(kind: LedgerKind, key: string, note = ''): LedgerEvent {
    this.seq += 1;
    const event: LedgerEvent = { seq: this.seq, kind, key, note };
    this.events.push(event);
    return event;
  }

  all(): readonly LedgerEvent[] {
    return this.events;
  }

  counts(): Record<LedgerKind, number> {
    const result = {} as Record<LedgerKind, number>;
    for (const kind of LEDGER_KINDS) result[kind] = 0;
    for (const event of this.events) result[event.kind] += 1;
    return result;
  }

  /** 对齐表格输出，便于人工核对。 */
  format(): string {
    const header = ['seq', 'kind', 'key', 'note'];
    const rows = this.events.map((e) => [String(e.seq), e.kind, e.key, e.note]);
    const widths = header.map((h, i) => Math.max(h.length, ...rows.map((r) => r[i].length)));
    const render = (cols: readonly string[]): string =>
      cols.map((c, i) => c.padEnd(widths[i])).join(' | ').replace(/\s+$/, '');
    const separator = widths.map((w) => '-'.repeat(w)).join('-+-');
    return [render(header), separator, ...rows.map(render)].join('\n');
  }
}
