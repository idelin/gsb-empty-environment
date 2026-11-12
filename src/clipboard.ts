import type { ClipboardAccessContext, SystemClipboard } from './types.ts';

export interface ClipboardCall {
  readonly mode: 'read' | 'write';
  readonly text: string;
  readonly context: ClipboardAccessContext;
  readonly at: number;
}

/**
 * 系统剪贴板记录替身：每一次真实读写都留痕，
 * 测试可逐条核对调用上下文，证明没有越权（无授权/错授权/错标签页）调用。
 */
export class RecordingSystemClipboard implements SystemClipboard {
  private stored = '';
  readonly calls: ClipboardCall[] = [];

  /** 可选：让下一次操作抛错，模拟底层剪贴板失败。 */
  failNextWith: string | null = null;

  constructor(initial = '') {
    this.stored = initial;
  }

  read(context: ClipboardAccessContext): string {
    this.record('read', this.stored, context);
    return this.stored;
  }

  write(text: string, context: ClipboardAccessContext): void {
    this.record('write', text, context);
    this.stored = text;
  }

  readContents(): string {
    return this.stored;
  }

  totalCalls(): number {
    return this.calls.length;
  }

  callsFor(mode: 'read' | 'write'): ClipboardCall[] {
    return this.calls.filter((call) => call.mode === mode);
  }

  /** 断言辅助：核对每条调用是否都持有匹配的单次授权。 */
  callContexts(): ClipboardAccessContext[] {
    return this.calls.map((call) => call.context);
  }

  private record(
    mode: 'read' | 'write',
    text: string,
    context: ClipboardAccessContext,
  ): void {
    if (this.failNextWith !== null) {
      const message = this.failNextWith;
      this.failNextWith = null;
      throw new Error(message);
    }
    this.calls.push({
      mode,
      text,
      context,
      at: this.calls.length,
    });
  }
}
