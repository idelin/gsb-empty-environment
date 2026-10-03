import type { Ballot, LogEntry } from './types.ts';

export type Message =
  | { type: 'prepare'; from: number; to: number; ballot: Ballot; fromIndex: number }
  | {
      type: 'promise';
      from: number;
      to: number;
      ballot: Ballot;
      entries: Array<[number, LogEntry]>;
      commitIndex: number;
    }
  | { type: 'reject'; from: number; to: number; ballot: Ballot; promised: Ballot; inReplyTo: 'prepare' | 'accept' }
  | {
      type: 'accept';
      from: number;
      to: number;
      ballot: Ballot;
      index: number;
      entry: LogEntry;
      commitIndex: number;
      leaderLeaseUntil: number;
    }
  | { type: 'accepted'; from: number; to: number; ballot: Ballot; index: number }
  | { type: 'commit'; from: number; to: number; ballot: Ballot; commitIndex: number; leaderLeaseUntil: number }
  | { type: 'catchup'; from: number; to: number; fromIndex: number };

/**
 * Synchronous, fully controllable in-memory network.
 * Directed links can be cut individually to emulate partitions and
 * one-way message loss. Every message is recorded for assertions.
 */
export class Network {
  readonly sent: Message[] = [];
  private readonly links = new Map<string, boolean>();
  private readonly handlers = new Map<number, (msg: Message) => void>();

  register(id: number, handler: (msg: Message) => void): void {
    this.handlers.set(id, handler);
  }

  private key(from: number, to: number): string {
    return `${from}->${to}`;
  }

  setLink(from: number, to: number, up: boolean): void {
    this.links.set(this.key(from, to), up);
  }

  isUp(from: number, to: number): boolean {
    return this.links.get(this.key(from, to)) ?? true;
  }

  /** Cut every directed link crossing the two groups. */
  partition(groupA: number[], groupB: number[]): void {
    for (const a of groupA) {
      for (const b of groupB) {
        this.setLink(a, b, false);
        this.setLink(b, a, false);
      }
    }
  }

  healAll(): void {
    this.links.clear();
  }

  send(msg: Message): void {
    this.sent.push(msg);
    if (!this.isUp(msg.from, msg.to)) return;
    this.handlers.get(msg.to)?.(msg);
  }

  count(type: Message['type']): number {
    return this.sent.filter((m) => m.type === type).length;
  }
}
