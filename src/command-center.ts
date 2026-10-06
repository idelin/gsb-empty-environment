import { ContextKeyService } from './context.ts';
import { EventLog, type CommandEvent } from './events.ts';
import { FocusManager } from './focus.ts';
import {
  CommandRegistry,
  type CommandSpec,
  type ListResult,
} from './registry.ts';

/** 门面：所有变更都经过它，以便完整记录事件日志供重放。 */
export class CommandCenter {
  readonly log = new EventLog();
  readonly context = new ContextKeyService();
  readonly registry: CommandRegistry;
  readonly focusManager: FocusManager;

  constructor() {
    const emit = (event: CommandEvent): void => this.log.record(event);
    this.registry = new CommandRegistry(this.context, emit);
    this.focusManager = new FocusManager(this.context, emit);
  }

  setContext(key: string, value: unknown): void {
    if (this.context.set(key, value)) {
      this.log.record({ type: 'context.set', key, value, version: this.context.version });
    }
  }

  unsetContext(key: string): void {
    if (this.context.unset(key)) {
      this.log.record({ type: 'context.unset', key, version: this.context.version });
    }
  }

  focus(part: string): void {
    this.focusManager.focus(part);
  }

  blur(): void {
    this.focusManager.blur();
  }

  register(spec: CommandSpec): void {
    this.registry.register(spec);
  }

  unregister(id: string): boolean {
    return this.registry.unregister(id);
  }

  list(): ListResult {
    return this.registry.list();
  }

  execute(id: string, args: unknown[] = [], listedVersion?: number): unknown {
    return this.registry.execute(id, args, listedVersion);
  }
}
