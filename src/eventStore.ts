import { closeSync, existsSync, fsyncSync, openSync, readFileSync, writeSync } from 'node:fs';
import type { NewEvent, StepEvent } from './events.ts';

export class EventStore {
  readonly path: string;
  private readonly workflowId: string;
  private fd: number;
  private seq = 0;

  constructor(path: string, workflowId: string) {
    this.path = path;
    this.workflowId = workflowId;
    if (existsSync(path)) {
      this.seq = this.readAll().length;
    }
    this.fd = openSync(path, 'a');
  }

  append(event: NewEvent): StepEvent {
    const full: StepEvent = {
      seq: ++this.seq,
      at: new Date().toISOString(),
      workflowId: this.workflowId,
      type: event.type,
      ...(event.stepId !== undefined ? { stepId: event.stepId } : {}),
      ...(event.detail !== undefined ? { detail: event.detail } : {}),
    };
    writeSync(this.fd, JSON.stringify(full) + '\n');
    fsyncSync(this.fd);
    return full;
  }

  readAll(): StepEvent[] {
    if (!existsSync(this.path)) return [];
    return readFileSync(this.path, 'utf8')
      .split('\n')
      .filter((line) => line.trim().length > 0)
      .map((line) => JSON.parse(line) as StepEvent);
  }

  close(): void {
    closeSync(this.fd);
  }
}
