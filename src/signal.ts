/**
 * 响应式 Signal：引擎依赖追踪的最小单元。
 * 在绑定求值上下文中读取 signal() 会自动建立依赖；
 * signal.set() 只精准通知依赖它的绑定，而非触发组件级重渲染。
 */

export type Subscriber = () => void;

export interface Signal<T> {
  /** 读取当前值（在绑定求值上下文中会建立依赖关系） */
  (): T;
  /** 写入新值；值变化时精准通知依赖它的绑定 */
  set(value: T): void;
  /** 读取但不建立依赖 */
  peek(): T;
  /** 手动订阅，返回取消函数 */
  subscribe(fn: Subscriber): () => void;
}

/** 当前正在求值的观察者（由引擎在求值绑定前压栈） */
let currentObserver: Subscriber | null = null;

export function withObserver<T>(observer: Subscriber, fn: () => T): T {
  const prev = currentObserver;
  currentObserver = observer;
  try {
    return fn();
  } finally {
    currentObserver = prev;
  }
}

export function signal<T>(initial: T): Signal<T> {
  let value = initial;
  const subscribers = new Set<Subscriber>();

  const read = (() => {
    if (currentObserver) subscribers.add(currentObserver);
    return value;
  }) as Signal<T>;

  read.set = (next: T) => {
    if (Object.is(next, value)) return;
    value = next;
    // 拷贝后遍历，允许通知过程中增删订阅
    for (const sub of [...subscribers]) sub();
  };

  read.peek = () => value;

  read.subscribe = (fn: Subscriber) => {
    subscribers.add(fn);
    return () => subscribers.delete(fn);
  };

  return read;
}
