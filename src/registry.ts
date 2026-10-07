import { ContextStore } from './context.ts';
import {
  CommandNotFoundError,
  CommandUnavailableError,
  ContextResolutionError,
  DuplicateCommandError,
  StaleContextError,
  UnknownContextKeyError,
} from './errors.ts';
import type { RegistryEvent } from './events.ts';
import { evaluateExpression, parseExpression } from './expression.ts';
import type { ExprNode } from './expression.ts';

export type CommandHandler = (args?: unknown) => unknown;

export interface CommandSpec {
  id: string;
  priority?: number;
  when?: string;
  run?: CommandHandler;
}

export interface ListedCommand {
  id: string;
  priority: number;
  when: string;
}

export interface AvailabilityList {
  contextVersion: number;
  commands: ListedCommand[];
  errors: { id: string; error: ContextResolutionError }[];
}

export interface ExecuteOptions {
  expectedContextVersion?: number;
  args?: unknown;
}

export interface ReplayOptions {
  handlers?: Record<string, CommandHandler>;
  onStep?: (event: RegistryEvent, registry: CommandRegistry, index: number) => void;
}

interface CommandEntry {
  id: string;
  priority: number;
  whenSource: string;
  whenAst: ExprNode;
  run?: CommandHandler;
}

const ALWAYS: ExprNode = { kind: 'literal', value: true };

export class CommandRegistry {
  #context = new ContextStore();
  #commands = new Map<string, CommandEntry>();
  #events: RegistryEvent[] = [];

  get contextVersion(): number {
    return this.#context.version;
  }

  get events(): readonly RegistryEvent[] {
    return this.#events;
  }

  static replay(events: readonly RegistryEvent[], options: ReplayOptions = {}): CommandRegistry {
    const registry = new CommandRegistry();
    events.forEach((event, index) => {
      const handler = event.type === 'command-registered' ? options.handlers?.[event.id] : undefined;
      registry.#apply(event, handler);
      options.onStep?.(event, registry, index);
    });
    return registry;
  }

  register(spec: CommandSpec): void {
    if (this.#commands.has(spec.id)) throw new DuplicateCommandError(spec.id);
    const whenSource = spec.when ?? 'true';
    parseExpression(whenSource);
    this.#apply(
      { type: 'command-registered', id: spec.id, priority: spec.priority ?? 0, when: whenSource },
      spec.run,
    );
  }

  unregister(id: string): void {
    if (!this.#commands.has(id)) throw new CommandNotFoundError(id);
    this.#apply({ type: 'command-unregistered', id });
  }

  setContext(key: string, value: boolean): void {
    this.#apply({ type: 'context-set', key, value });
  }

  unsetContext(key: string): void {
    this.#apply({ type: 'context-unset', key });
  }

  listAvailable(): AvailabilityList {
    const commands: ListedCommand[] = [];
    const errors: AvailabilityList['errors'] = [];
    for (const entry of this.#commands.values()) {
      try {
        if (evaluateExpression(entry.whenAst, (key) => this.#resolveKey(key))) {
          commands.push({ id: entry.id, priority: entry.priority, when: entry.whenSource });
        }
      } catch (error) {
        if (error instanceof UnknownContextKeyError) {
          errors.push({ id: entry.id, error: new ContextResolutionError(entry.id, error.key) });
        } else {
          throw error;
        }
      }
    }
    commands.sort(
      (a, b) => b.priority - a.priority || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0),
    );
    return { contextVersion: this.#context.version, commands, errors };
  }

  execute(id: string, options: ExecuteOptions = {}): unknown {
    const entry = this.#commands.get(id);
    if (!entry) throw new CommandNotFoundError(id);
    const { expectedContextVersion, args } = options;
    if (expectedContextVersion !== undefined && expectedContextVersion !== this.#context.version) {
      throw new StaleContextError(expectedContextVersion, this.#context.version);
    }
    let available: boolean;
    try {
      available = evaluateExpression(entry.whenAst, (key) => this.#resolveKey(key));
    } catch (error) {
      if (error instanceof UnknownContextKeyError) {
        throw new ContextResolutionError(id, error.key);
      }
      throw error;
    }
    if (!available) throw new CommandUnavailableError(id);
    return entry.run?.(args);
  }

  snapshot(): {
    contextVersion: number;
    context: Record<string, boolean>;
    commands: { id: string; priority: number; when: string }[];
  } {
    return {
      contextVersion: this.#context.version,
      context: this.#context.snapshot(),
      commands: [...this.#commands.values()]
        .map(({ id, priority, whenSource }) => ({ id, priority, when: whenSource }))
        .sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0)),
    };
  }

  #resolveKey(key: string): boolean {
    const value = this.#context.get(key);
    if (value === undefined) throw new UnknownContextKeyError(key);
    return value;
  }

  #apply(event: RegistryEvent, run?: CommandHandler): void {
    switch (event.type) {
      case 'command-registered':
        this.#commands.set(event.id, {
          id: event.id,
          priority: event.priority,
          whenSource: event.when,
          whenAst: parseExpression(event.when),
          run,
        });
        break;
      case 'command-unregistered':
        this.#commands.delete(event.id);
        break;
      case 'context-set':
        this.#context.set(event.key, event.value);
        break;
      case 'context-unset':
        this.#context.unset(event.key);
        break;
    }
    this.#events.push(event);
  }
}
