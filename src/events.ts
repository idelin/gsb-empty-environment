/** 命令库产生的全部事件；输入类事件可重放，观察类事件用于断言。 */
export type CommandEvent =
  | { type: 'context.set'; key: string; value: unknown; version: number }
  | { type: 'context.unset'; key: string; version: number }
  | { type: 'focus.changed'; from: string | null; to: string | null }
  | { type: 'command.registered'; id: string; priority: number; when?: string }
  | { type: 'command.unregistered'; id: string }
  | { type: 'command.listed'; version: number; available: string[] }
  | { type: 'command.executed'; id: string; revalidated: boolean }
  | { type: 'command.executeRejected'; id: string; reason: 'not-found' | 'unavailable'; revalidated: boolean }
  | { type: 'condition.parseError'; id: string; when: string; message: string; offset: number };

export class EventLog {
  #events: CommandEvent[] = [];

  record(event: CommandEvent): void {
    this.#events.push(event);
  }

  get events(): readonly CommandEvent[] {
    return this.#events;
  }

  ofType<T extends CommandEvent['type']>(
    type: T,
  ): Extract<CommandEvent, { type: T }>[] {
    return this.#events.filter((e): e is Extract<CommandEvent, { type: T }> => e.type === type);
  }
}
