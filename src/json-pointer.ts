// RFC 6901 JSON Pointer 的最小实现，用于解析结果引用中的 path。

export class JsonPointerError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "JsonPointerError";
  }
}

/** 把 RFC 6901 pointer 解码成 token 数组。 */
export function parsePointer(pointer: string): string[] {
  if (pointer === "") {
    return [];
  }
  if (!pointer.startsWith("/")) {
    throw new JsonPointerError(`非法 JSON Pointer（必须以 / 开头或为空串）: ${pointer}`);
  }
  return pointer
    .slice(1)
    .split("/")
    .map((token) => token.replace(/~1/g, "/").replace(/~0/g, "~"));
}

/** 沿 pointer 取值；路径不存在或越界时抛出 JsonPointerError。 */
export function resolvePointer(
  document: unknown,
  pointer: string,
): unknown {
  const tokens = parsePointer(pointer);
  let current: unknown = document;
  for (const token of tokens) {
    if (current === null || typeof current !== "object") {
      throw new JsonPointerError(`路径段 ${token} 无法在非对象值上解析`);
    }
    if (Array.isArray(current)) {
      if (token === "-") {
        throw new JsonPointerError("路径段 - 指向数组末尾之后，无可取值");
      }
      const index = Number(token);
      if (!/^(0|[1-9][0-9]*)$/.test(token) || index >= current.length) {
        throw new JsonPointerError(`数组索引越界或非法: ${token}`);
      }
      current = current[index];
    } else {
      if (!Object.prototype.hasOwnProperty.call(current, token)) {
        throw new JsonPointerError(`对象上不存在属性: ${token}`);
      }
      current = (current as Record<string, unknown>)[token];
    }
  }
  return current;
}
