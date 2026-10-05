import type { JsonValue } from "./ast.ts";
import type { PathSeg } from "./path.ts";

export type TreeEvent =
  | { type: "open"; path: PathSeg[]; kind: "array" | "object" }
  | { type: "close"; path: PathSeg[]; kind: "array" | "object" }
  | { type: "value"; path: PathSeg[]; value: JsonValue };

type StackItem =
  | { t: "node"; value: JsonValue; path: PathSeg[] }
  | { t: "close"; path: PathSeg[]; kind: "array" | "object" };

/**
 * SAX-style event stream over a JSON tree. Iterative (explicit stack), so
 * arbitrarily deep trees do not overflow the call stack.
 */
export function* walkEvents(root: JsonValue): Generator<TreeEvent> {
  const stack: StackItem[] = [{ t: "node", value: root, path: [] }];
  while (stack.length > 0) {
    const item = stack.pop()!;
    if (item.t === "close") {
      yield { type: "close", path: item.path, kind: item.kind };
      continue;
    }
    const { value, path } = item;
    if (value === null || typeof value !== "object") {
      yield { type: "value", path, value };
      continue;
    }
    if (Array.isArray(value)) {
      yield { type: "open", path, kind: "array" };
      stack.push({ t: "close", path, kind: "array" });
      for (let i = value.length - 1; i >= 0; i--) {
        stack.push({ t: "node", value: value[i], path: [...path, i] });
      }
    } else {
      yield { type: "open", path, kind: "object" };
      stack.push({ t: "close", path, kind: "object" });
      const keys = Object.keys(value);
      for (let i = keys.length - 1; i >= 0; i--) {
        stack.push({ t: "node", value: value[keys[i]], path: [...path, keys[i]] });
      }
    }
  }
}

/** Rebuild the JSON value of a subtree from its recorded events. */
export function buildValue(events: Iterable<TreeEvent>): JsonValue {
  const stack: Array<{ container: JsonValue[] | { [key: string]: JsonValue }; kind: "array" | "object" }> = [];
  let result: JsonValue = null;
  const attach = (path: PathSeg[], v: JsonValue): void => {
    const top = stack[stack.length - 1];
    if (top === undefined) {
      result = v;
      return;
    }
    if (top.kind === "array") {
      (top.container as JsonValue[]).push(v);
    } else {
      (top.container as { [key: string]: JsonValue })[String(path[path.length - 1])] = v;
    }
  };
  for (const ev of events) {
    if (ev.type === "open") {
      const container: JsonValue = ev.kind === "array" ? [] : {};
      attach(ev.path, container);
      stack.push({ container: container as JsonValue[] & { [key: string]: JsonValue }, kind: ev.kind });
    } else if (ev.type === "value") {
      attach(ev.path, ev.value);
    } else {
      stack.pop();
    }
  }
  return result;
}
