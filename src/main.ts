import { Scenario, Segment, SimulationResult, TickRecord } from './types';
import { Scheduler } from './scheduler';

declare function require(name: string): any;
declare const process: { argv: string[] };
declare const console: { log(...args: any[]): void };

const fs = require('fs');

function parseSegment(raw: any): Segment {
  if (raw && typeof raw.run === 'number') return { kind: 'run', duration: raw.run };
  if (raw && typeof raw.lock === 'string') return { kind: 'lock', lock: raw.lock };
  if (raw && typeof raw.unlock === 'string') return { kind: 'unlock', lock: raw.unlock };
  throw new Error(`invalid segment: ${JSON.stringify(raw)} (want {run:N} | {lock:"L"} | {unlock:"L"})`);
}

export function parseScenario(raw: any): Scenario {
  if (!raw || !Array.isArray(raw.tasks)) {
    throw new Error('scenario must have a "tasks" array');
  }
  return {
    name: typeof raw.name === 'string' ? raw.name : 'unnamed',
    maxTicks: typeof raw.maxTicks === 'number' ? raw.maxTicks : undefined,
    tasks: raw.tasks.map((task: any) => {
      if (typeof task.id !== 'string' || typeof task.priority !== 'number') {
        throw new Error(`each task needs "id" (string) and "priority" (number): ${JSON.stringify(task)}`);
      }
      if (!Array.isArray(task.segments)) {
        throw new Error(`task ${task.id} needs a "segments" array`);
      }
      return {
        id: task.id,
        priority: task.priority,
        releaseTime: typeof task.releaseTime === 'number' ? task.releaseTime : 0,
        lockTimeout: typeof task.lockTimeout === 'number' ? task.lockTimeout : undefined,
        segments: task.segments.map(parseSegment),
      };
    }),
  };
}

function cpuLabel(record: TickRecord): string {
  if (record.running !== null) {
    return `RUN ${record.running} (base=${record.basePriority}, eff=${record.effectivePriority})`;
  }
  if (record.events.some((e) => e.indexOf('DEADLOCK') === 0)) {
    return 'DEADLOCK (no runnable task)';
  }
  if (record.blocked.length > 0) {
    return 'STALLED (tasks blocked, no runnable task)';
  }
  return 'IDLE';
}

export function render(result: SimulationResult): string {
  const lines: string[] = [];
  lines.push(`=== Scenario: ${result.name} ===`);
  for (const record of result.ticks) {
    const tick = `t=${String(record.tick).padStart(3, '0')}`;
    let line = `${tick} | ${cpuLabel(record)}`;
    if (record.blocked.length > 0) {
      const parts = record.blocked.map((b) => {
        const timeout = b.timeout !== undefined ? `/${b.timeout}` : '';
        return `${b.id}->${b.lock}(held by ${b.holder === null ? 'none' : b.holder}, waited ${b.waited}${timeout})`;
      });
      line += ` | blocked: ${parts.join(', ')}`;
    }
    lines.push(line);
    for (const event of record.events) {
      lines.push(`       ! ${event}`);
    }
  }
  lines.push('--- Task summary ---');
  for (const task of result.tasks) {
    const at = task.finishTick !== null ? ` @t=${task.finishTick}` : '';
    lines.push(`  ${task.id}: ${task.status}${at}`);
  }
  return lines.join('\n');
}

const demos: Scenario[] = [
  {
    name: 'priority-inversion + inheritance',
    tasks: [
      {
        id: 'L', priority: 1, releaseTime: 0,
        segments: [
          { kind: 'lock', lock: 'A' }, { kind: 'run', duration: 3 },
          { kind: 'unlock', lock: 'A' }, { kind: 'run', duration: 1 },
        ],
      },
      {
        id: 'M', priority: 5, releaseTime: 1,
        segments: [{ kind: 'run', duration: 6 }],
      },
      {
        id: 'H', priority: 9, releaseTime: 2,
        segments: [
          { kind: 'lock', lock: 'A' }, { kind: 'run', duration: 1 }, { kind: 'unlock', lock: 'A' },
        ],
      },
    ],
  },
  {
    name: 'transitive priority inheritance (C -> B -> A)',
    tasks: [
      {
        id: 'T1', priority: 1, releaseTime: 0,
        segments: [
          { kind: 'lock', lock: 'L1' }, { kind: 'run', duration: 5 }, { kind: 'unlock', lock: 'L1' },
        ],
      },
      {
        id: 'T2', priority: 5, releaseTime: 1,
        segments: [
          { kind: 'lock', lock: 'L2' }, { kind: 'run', duration: 1 },
          { kind: 'lock', lock: 'L1' }, { kind: 'run', duration: 1 },
          { kind: 'unlock', lock: 'L1' }, { kind: 'unlock', lock: 'L2' },
        ],
      },
      {
        id: 'T3', priority: 9, releaseTime: 2,
        segments: [
          { kind: 'lock', lock: 'L2' }, { kind: 'run', duration: 1 }, { kind: 'unlock', lock: 'L2' },
        ],
      },
    ],
  },
  {
    name: 'lock wait timeout',
    tasks: [
      {
        id: 'L', priority: 1, releaseTime: 0,
        segments: [
          { kind: 'lock', lock: 'A' }, { kind: 'run', duration: 6 }, { kind: 'unlock', lock: 'A' },
        ],
      },
      {
        id: 'H', priority: 9, releaseTime: 1, lockTimeout: 3,
        segments: [
          { kind: 'lock', lock: 'A' }, { kind: 'run', duration: 1 }, { kind: 'unlock', lock: 'A' },
        ],
      },
    ],
  },
  {
    name: 'deadlock (lock wait cycle)',
    tasks: [
      {
        id: 'A', priority: 5, releaseTime: 0,
        segments: [
          { kind: 'lock', lock: 'L1' }, { kind: 'run', duration: 2 },
          { kind: 'lock', lock: 'L2' }, { kind: 'run', duration: 1 },
          { kind: 'unlock', lock: 'L2' }, { kind: 'unlock', lock: 'L1' },
        ],
      },
      {
        id: 'B', priority: 6, releaseTime: 1,
        segments: [
          { kind: 'lock', lock: 'L2' }, { kind: 'run', duration: 1 },
          { kind: 'lock', lock: 'L1' }, { kind: 'run', duration: 1 },
          { kind: 'unlock', lock: 'L1' }, { kind: 'unlock', lock: 'L2' },
        ],
      },
    ],
  },
];

function main(): void {
  const file = process.argv[2];
  if (file) {
    const scenario = parseScenario(JSON.parse(fs.readFileSync(file, 'utf8')));
    console.log(render(new Scheduler(scenario).run()));
    return;
  }
  for (const demo of demos) {
    console.log(render(new Scheduler(demo).run()));
    console.log('');
  }
}

main();
