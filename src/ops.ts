// 状态操作的正向应用与逆补丁计算。
import type { StateOp, Patch, StateValue } from './types.ts';
import { deepClone } from './hash.ts';

export type State = Record<string, StateValue>;

export function applyOp(state: State, op: StateOp): void {
  switch (op.kind) {
    case 'kv.set':
      state[op.key] = deepClone(op.value);
      return;
    case 'kv.add': {
      const cur = state[op.key];
      const base = typeof cur === 'number' ? cur : 0;
      state[op.key] = base + op.delta;
      return;
    }
  }
}

// 依据当前状态计算 op 的逆补丁（写入日志，供反向步进使用）。
export function invertOp(state: State, op: StateOp): Patch[] {
  switch (op.kind) {
    case 'kv.set':
      return [{ key: op.key, before: deepClone(state[op.key]), after: deepClone(op.value) }];
    case 'kv.add': {
      const cur = state[op.key];
      const base = typeof cur === 'number' ? cur : 0;
      return [{ key: op.key, before: deepClone(cur), after: base + op.delta }];
    }
  }
}

export function applyInverse(state: State, patches: Patch[]): void {
  for (let i = patches.length - 1; i >= 0; i--) {
    const p = patches[i];
    if (p.before === undefined) delete state[p.key];
    else state[p.key] = deepClone(p.before);
  }
}

export function applyForward(state: State, patches: Patch[]): void {
  for (const p of patches) {
    if (p.after === undefined) delete state[p.key];
    else state[p.key] = deepClone(p.after);
  }
}
