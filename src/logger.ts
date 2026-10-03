export interface LogEntry {
  tick: number;
  node: string;
  message: string;
}

export class EventLog {
  entries: LogEntry[] = [];

  add(tick: number, node: string, message: string): void {
    this.entries.push({ tick, node, message });
  }

  since(index: number): LogEntry[] {
    return this.entries.slice(index);
  }

  count(pattern: RegExp): number {
    return this.entries.filter((e) => pattern.test(e.message)).length;
  }

  format(entries: LogEntry[] = this.entries): string[] {
    return entries.map(
      (e) => `[tick ${String(e.tick).padStart(3)}] ${e.node.padEnd(6)} ${e.message}`,
    );
  }
}
