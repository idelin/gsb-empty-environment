import { ContextKeyService } from './context.ts';
import type { CommandEvent } from './events.ts';

/**
 * 焦点管理：切换焦点时维护 `focus` 字符串键与 `focus.<part>` 布尔键，
 * 供 when 表达式使用（如 `focus.editor`、`focus == 'terminal'`）。
 */
export class FocusManager {
  #parts = new Set<string>();
  #current: string | null = null;

  private readonly context: ContextKeyService;
  private readonly emit: (event: CommandEvent) => void;

  constructor(context: ContextKeyService, emit: (event: CommandEvent) => void = () => {}) {
    this.context = context;
    this.emit = emit;
  }

  get current(): string | null {
    return this.#current;
  }

  focus(part: string): void {
    if (part === this.#current) {
      return;
    }
    const from = this.#current;
    this.#parts.add(part);
    for (const known of this.#parts) {
      this.context.set(`focus.${known}`, known === part);
    }
    this.context.set('focus', part);
    this.#current = part;
    this.emit({ type: 'focus.changed', from, to: part });
  }

  blur(): void {
    if (this.#current === null) {
      return;
    }
    const from = this.#current;
    for (const known of this.#parts) {
      this.context.set(`focus.${known}`, false);
    }
    this.context.unset('focus');
    this.#current = null;
    this.emit({ type: 'focus.changed', from, to: null });
  }
}
