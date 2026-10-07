import * as fs from 'fs';
import * as path from 'path';

export type EventType =
  | 'RunStarted'
  | 'StepStarted'
  | 'StepSucceeded'
  | 'StepFailed'
  | 'RunFailed'
  | 'CompensationStarted'
  | 'CompensationSucceeded'
  | 'CompensationFailed'
  | 'RunCompensationFailed'
  | 'RunCompensated'
  | 'RunCompleted';

export interface Event {
  seq: number;
  runId: string;
  type: EventType;
  stepId?: string;
  error?: string;
  ts: number;
}

/**
 * Persistent append-only event table backed by a JSONL file.
 * Every state transition is durably appended before the executor
 * advances, so a crash never loses an already-acknowledged receipt.
 */
export class EventStore {
  private events: Event[] = [];

  constructor(private readonly file: string) {
    if (fs.existsSync(file)) {
      const lines = fs.readFileSync(file, 'utf8').split('\n').filter((l) => l.trim().length > 0);
      for (const line of lines) this.events.push(JSON.parse(line) as Event);
    } else {
      fs.mkdirSync(path.dirname(file), { recursive: true });
      fs.writeFileSync(file, '');
    }
  }

  append(type: EventType, runId: string, stepId?: string, error?: string): Event {
    const event: Event = {
      seq: this.events.length + 1,
      runId,
      type,
      stepId,
      error,
      ts: Date.now(),
    };
    fs.appendFileSync(this.file, JSON.stringify(event) + '\n');
    this.events.push(event);
    return event;
  }

  forRun(runId: string): Event[] {
    return this.events.filter((e) => e.runId === runId);
  }

  all(): Event[] {
    return [...this.events];
  }
}
