import { ContextKeyService } from './context.ts';
import { ConditionParseError, parseCondition, type Expr } from './expression.ts';
import type { CommandEvent } from './events.ts';

export type CommandHandler = (...args: unknown[]) => unknown;

export interface CommandSpec {
  id: string;
  handler: CommandHandler;
  /** 用户优先级，越大越靠前，默认 0。 */
  priority?: number;
  /** 有限布尔上下文表达式，缺省表示始终可用。 */
  when?: string;
  title?: string;
}

export class CommandNotFoundError extends Error {
  readonly commandId: string;

  constructor(commandId: string) {
    super(`command not found: ${commandId}`);
    this.name = 'CommandNotFoundError';
    this.commandId = commandId;
  }
}

export class CommandUnavailableError extends Error {
  readonly commandId: string;

  constructor(commandId: string) {
    super(`command is not available in the current context: ${commandId}`);
    this.name = 'CommandUnavailableError';
    this.commandId = commandId;
  }
}

interface CommandEntry {
  id: string;
  handler: CommandHandler;
  priority: number;
  title?: string;
  when?: string;
  expr?: Expr;
}

export interface ListedCommand {
  id: string;
  priority: number;
  title?: string;
}

export interface ListResult {
  /** 生成该列表时的上下文版本。 */
  version: number;
  commands: ListedCommand[];
}

/**
 * 命令注册表。list() 的可用性只是当时上下文下的快照；
 * execute() 总会基于当前上下文重新求值，listedVersion 用于检测快照过期。
 */
export class CommandRegistry {
  #entries = new Map<string, CommandEntry>();
  private readonly context: ContextKeyService;
  private readonly emit: (event: CommandEvent) => void;

  constructor(context: ContextKeyService, emit: (event: CommandEvent) => void = () => {}) {
    this.context = context;
    this.emit = emit;
  }

  register(spec: CommandSpec): void {
    if (!spec.id) {
      throw new Error('command id must be non-empty');
    }
    let expr: Expr | undefined;
    if (spec.when !== undefined) {
      try {
        expr = parseCondition(spec.when);
      } catch (err) {
        if (err instanceof ConditionParseError) {
          this.emit({
            type: 'condition.parseError',
            id: spec.id,
            when: spec.when,
            message: err.message,
            offset: err.offset,
          });
        }
        throw err;
      }
    }
    const priority = spec.priority ?? 0;
    this.#entries.set(spec.id, {
      id: spec.id,
      handler: spec.handler,
      priority,
      title: spec.title,
      when: spec.when,
      expr,
    });
    this.emit({ type: 'command.registered', id: spec.id, priority, when: spec.when });
  }

  unregister(id: string): boolean {
    const removed = this.#entries.delete(id);
    if (removed) {
      this.emit({ type: 'command.unregistered', id });
    }
    return removed;
  }

  has(id: string): boolean {
    return this.#entries.has(id);
  }

  private isAvailable(entry: CommandEntry): boolean {
    return entry.expr === undefined || this.context.evaluate(entry.expr);
  }

  /** 可用命令按用户优先级降序、再按 id 升序排列。 */
  list(): ListResult {
    const available = [...this.#entries.values()].filter((entry) => this.isAvailable(entry));
    available.sort((a, b) => b.priority - a.priority || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
    const commands: ListedCommand[] = available.map(({ id, priority, title }) => ({
      id,
      priority,
      title,
    }));
    const version = this.context.version;
    this.emit({ type: 'command.listed', version, available: commands.map((c) => c.id) });
    return { version, commands };
  }

  /**
   * 执行命令。无论是否提供 listedVersion，都会基于当前上下文重新求值；
   * listedVersion 与当前版本不一致时标记 revalidated（快照已过期，不保证仍可执行）。
   */
  execute(id: string, args: unknown[] = [], listedVersion?: number): unknown {
    const revalidated = listedVersion !== undefined && listedVersion !== this.context.version;
    const entry = this.#entries.get(id);
    if (!entry) {
      this.emit({ type: 'command.executeRejected', id, reason: 'not-found', revalidated });
      throw new CommandNotFoundError(id);
    }
    if (!this.isAvailable(entry)) {
      this.emit({ type: 'command.executeRejected', id, reason: 'unavailable', revalidated });
      throw new CommandUnavailableError(id);
    }
    this.emit({ type: 'command.executed', id, revalidated });
    return entry.handler(...args);
  }
}
