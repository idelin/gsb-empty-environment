import type { Store, Snapshot } from '../src/index.ts';

/** 可 Abort 的延时，模拟 fetch。 */
export function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      signal?.removeEventListener('abort', onAbort);
      resolve();
    }, ms);
    const onAbort = (): void => {
      clearTimeout(timer);
      reject(new DOMException('The operation was aborted.', 'AbortError'));
    };
    if (signal?.aborted) {
      clearTimeout(timer);
      onAbort();
      return;
    }
    signal?.addEventListener('abort', onAbort, { once: true });
  });
}

/** 等待某个 key 的快照值满足条件。 */
export function waitFor<T>(
  store: Store,
  key: string,
  pred: (value: T) => boolean,
  timeoutMs = 2000,
): Promise<T> {
  return new Promise((resolve, reject) => {
    const check = (snapshot: Snapshot): void => {
      const value = snapshot[key] as T;
      if (pred(value)) {
        unsubscribe();
        clearTimeout(timer);
        resolve(value);
      }
    };
    const unsubscribe = store.subscribe(check);
    const timer = setTimeout(() => {
      unsubscribe();
      reject(new Error(`waitFor("${key}") 超时，当前值：${JSON.stringify(store.snapshot()[key])}`));
    }, timeoutMs);
    check(store.snapshot());
  });
}
