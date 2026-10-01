/**
 * 键盘事件回放引擎。
 * 每个步骤后校验不变量：无焦点丢失、读屏公告与焦点同步；
 * 调度器本身对任务数设上限，从机制上暴露循环等待。
 */

import type { FocusNavigator } from './navigator.ts';
import type { Scheduler } from './scheduler.ts';

export type ReplayStep =
  | { kind: 'key'; key: string }
  | { kind: 'advance'; ms: number }
  | { kind: 'runAll' }
  | { kind: 'mutate'; note: string; fn: () => void }
  | { kind: 'expectFocus'; id: string };

export interface ReplayContext {
  navigator: FocusNavigator;
  scheduler: Scheduler;
}

export interface ReplayReport {
  ok: boolean;
  violations: string[];
  log: string[];
}

export function replay(ctx: ReplayContext, steps: ReplayStep[]): ReplayReport {
  const violations: string[] = [];
  const log: string[] = [];
  const check = (label: string) => {
    for (const v of ctx.navigator.checkInvariants()) {
      violations.push(`[${label}] ${v}`);
    }
  };
  steps.forEach((step, index) => {
    const label = `step ${index}`;
    switch (step.kind) {
      case 'key':
        ctx.navigator.pressKeyEvent(step.key);
        log.push(`${label} key ${step.key} -> focus ${ctx.navigator.focusId}`);
        break;
      case 'advance':
        ctx.scheduler.advance(step.ms);
        log.push(`${label} advance ${step.ms}ms -> focus ${ctx.navigator.focusId}`);
        break;
      case 'runAll':
        ctx.scheduler.runAll();
        log.push(`${label} runAll -> focus ${ctx.navigator.focusId}`);
        break;
      case 'mutate':
        step.fn();
        log.push(`${label} mutate(${step.note}) -> focus ${ctx.navigator.focusId}`);
        break;
      case 'expectFocus':
        if (ctx.navigator.focusId !== step.id) {
          violations.push(`[${label}] expectFocus ${step.id} but got ${ctx.navigator.focusId}`);
        }
        log.push(`${label} expectFocus ${step.id} ${ctx.navigator.focusId === step.id ? 'OK' : 'FAIL'}`);
        break;
    }
    check(label);
  });
  return { ok: violations.length === 0, violations, log };
}
