/** JSON 兼容树：对象键无序、数组有序。 */
export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

export type JsonObject = { [key: string]: JsonValue };

/** 定位到树中具体位置的路径段：字符串为对象键，数字为数组下标。 */
export type PathSegment = string | number;
export type Path = readonly PathSegment[];

export function isJsonObject(value: JsonValue): value is JsonObject {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** 将路径格式化为 `$.a.b[0]` 形式。 */
export function formatPath(path: Path): string {
  let out = "$";
  for (const seg of path) {
    if (typeof seg === "number") {
      out += `[${seg}]`;
    } else if (/^[A-Za-z_$][A-Za-z0-9_$]*$/.test(seg)) {
      out += `.${seg}`;
    } else {
      out += `[${JSON.stringify(seg)}]`;
    }
  }
  return out;
}

/** 按路径取值；路径不存在时返回 undefined。 */
export function getAtPath(root: JsonValue, path: Path): JsonValue | undefined {
  let node: JsonValue | undefined = root;
  for (const seg of path) {
    if (node === undefined || node === null || typeof node !== "object") {
      return undefined;
    }
    if (Array.isArray(node)) {
      if (typeof seg !== "number" || seg < 0 || seg >= node.length) return undefined;
      node = node[seg];
    } else {
      if (typeof seg !== "string" || !(seg in node)) return undefined;
      node = node[seg];
    }
  }
  return node;
}

/** 结构化深比较：对象键序无关，数组顺序敏感。 */
export function jsonEquals(a: JsonValue, b: JsonValue): boolean {
  if (a === b) return true;
  if (typeof a !== "object" || typeof b !== "object" || a === null || b === null) {
    return false;
  }
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    return a.every((item, i) => jsonEquals(item, b[i]));
  }
  const keysA = Object.keys(a);
  const keysB = Object.keys(b);
  if (keysA.length !== keysB.length) return false;
  return keysA.every((k) => k in b && jsonEquals(a[k], b[k]));
}
