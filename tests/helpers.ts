import { Registry } from '../src/registry.ts';
import { Store } from '../src/store.ts';
import type { ComputationDef } from '../src/registry.ts';

export const addDef: ComputationDef = {
  id: 'add',
  version: 1,
  compute: ([a, b]) => (a as number) + (b as number),
};

export const mulDef: ComputationDef = {
  id: 'mul',
  version: 1,
  compute: ([a, b]) => (a as number) * (b as number),
};

/** 单调累加器：允许指向自身的反馈边（单调环）。 */
export const accDef: ComputationDef = {
  id: 'acc',
  version: 1,
  monotonic: true,
  initial: 0,
  compute: ([prev, x]) => (prev as number) + (x as number),
};

export function makeRegistry(): Registry {
  return new Registry().register(addDef).register(mulDef).register(accDef);
}

/** a=1, b=2, sum=a+b, product=a*b, total=acc(total, a) */
export function makeStore(registry: Registry = makeRegistry()): Store {
  const store = new Store(registry);
  store
    .begin()
    .defineState('a', 1)
    .defineState('b', 2)
    .defineComputed('sum', addDef, ['a', 'b'])
    .defineComputed('product', mulDef, ['a', 'b'])
    .defineComputed('total', accDef, ['total', 'a'])
    .commit();
  return store;
}
