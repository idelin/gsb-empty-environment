/**
 * CircularSignalUpdate：响应式循环更新异常。
 * 当守护器检测到调用栈环路或迭代次数突破阈值时抛出，
 * 携带完整的循环因果链报告。
 */

export type CycleKind = 'iteration-overflow' | 'call-stack-cycle';

export interface SourceInfo {
  /** 写入方（Effect / Computed）的标签 */
  readonly observerLabel: string;
  /** 被写入的 Signal 标签 */
  readonly signalLabel: string;
  /** 自依赖关系：直接写入自身依赖，或经由 Computed 链间接写入 */
  readonly relation: '直接' | '间接';
}

export interface CircularReport {
  readonly kind: CycleKind;
  readonly iterations: number;
  readonly maxIterations: number;
  /** 被拦截时正准备执行的节点标签 */
  readonly trigger: string;
  /** 首个被检测到的自依赖写入（触发源），可能为 null（纯跨节点环路） */
  readonly source: SourceInfo | null;
  /** 因果链事件列表 */
  readonly chain: readonly string[];
}

const MAX_REPORT_EVENTS = 24;
const HEAD_EVENTS = 12;
const TAIL_EVENTS = 8;

function formatChain(chain: readonly string[]): string {
  const lines: string[] = [];
  if (chain.length <= MAX_REPORT_EVENTS) {
    chain.forEach((event, index) => {
      lines.push(`  ${String(index + 1).padStart(4)}. ${event}`);
    });
    return lines.join('\n');
  }
  const omitted = chain.length - HEAD_EVENTS - TAIL_EVENTS;
  chain.slice(0, HEAD_EVENTS).forEach((event, index) => {
    lines.push(`  ${String(index + 1).padStart(4)}. ${event}`);
  });
  lines.push(`   ... 省略中间 ${omitted} 条事件 ...`);
  chain.slice(-TAIL_EVENTS).forEach((event, index) => {
    lines.push(`  ${String(chain.length - TAIL_EVENTS + index + 1).padStart(4)}. ${event}`);
  });
  return lines.join('\n');
}

function buildMessage(report: CircularReport): string {
  const kindText =
    report.kind === 'iteration-overflow'
      ? `迭代次数突破阈值（在第 ${report.iterations} 次迭代时拦截，阈值 = ${report.maxIterations}）`
      : '检测到调用栈环路（同一响应式节点在执行栈上重入）';
  const sourceText = report.source
    ? `${report.source.observerLabel} 在执行中写入了自身依赖 ${report.source.signalLabel}（${report.source.relation}自依赖）`
    : `${report.trigger}（跨节点环路，未发现单节点自依赖写入）`;
  return [
    '检测到响应式循环更新（死循环），守护器已强制阻断。',
    `阻断原因: ${kindText}`,
    `触发源: ${sourceText}`,
    `被拦截节点: ${report.trigger}`,
    `循环因果链（共 ${report.chain.length} 条事件）:`,
    formatChain(report.chain),
  ].join('\n');
}

export class CircularSignalUpdate extends Error {
  override readonly name = 'CircularSignalUpdate';
  readonly report: CircularReport;

  constructor(report: CircularReport) {
    super(buildMessage(report));
    this.report = report;
  }

  get kind(): CycleKind {
    return this.report.kind;
  }

  get iterations(): number {
    return this.report.iterations;
  }

  get maxIterations(): number {
    return this.report.maxIterations;
  }

  get source(): SourceInfo | null {
    return this.report.source;
  }

  get chain(): readonly string[] {
    return this.report.chain;
  }
}
