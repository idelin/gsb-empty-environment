import type { JsonValue } from './ast.ts';
import { isObject } from './ast.ts';
import type { Path } from './path.ts';

export type NodeKind = 'object' | 'array' | 'primitive';

export function kindOf(v: JsonValue): NodeKind {
  if (Array.isArray(v)) return 'array';
  if (isObject(v)) return 'object';
  return 'primitive';
}

/** enter：节点开始（容器子节点尚未到达）；exit：节点完整（携带完整值）。 */
export type Event =
  | { type: 'enter'; path: Path; kind: NodeKind }
  | { type: 'exit'; path: Path; kind: NodeKind; value: JsonValue };

/**
 * 把 JSON 树转换为深度优先事件流（模拟大树 SAX 流）。
 * 迭代实现，显式栈，任意深度不爆栈。
 */
export function* walk(root: JsonValue): Generator<Event, void, undefined> {
  const rootKind = kindOf(root);
  yield { type: 'enter', path: [], kind: rootKind };
  if (rootKind === 'primitive') {
    yield { type: 'exit', path: [], kind: rootKind, value: root };
    return;
  }
  const stack: { value: JsonValue[] | Record<string, JsonValue>; path: Path; keys: (string | number)[]; i: number }[] = [];
  const pushFrame = (value: JsonValue, path: Path): void => {
    if (Array.isArray(value)) {
      stack.push({ value, path, keys: value.map((_, i) => i), i: 0 });
    } else {
      stack.push({ value: value as Record<string, JsonValue>, path, keys: Object.keys(value as object), i: 0 });
    }
  };
  pushFrame(root, []);
  while (stack.length > 0) {
    const top = stack[stack.length - 1]!;
    if (top.i < top.keys.length) {
      const key = top.keys[top.i++]!;
      const child = (top.value as Record<string | number, JsonValue>)[key] as JsonValue;
      const childPath = [...top.path, key];
      const ck = kindOf(child as JsonValue);
      yield { type: 'enter', path: childPath, kind: ck };
      if (ck === 'primitive') {
        yield { type: 'exit', path: childPath, kind: ck, value: child as JsonValue };
      } else {
        pushFrame(child as JsonValue, childPath);
      }
    } else {
      yield { type: 'exit', path: top.path, kind: kindOf(top.value as JsonValue), value: top.value as JsonValue };
      stack.pop();
    }
  }
}
