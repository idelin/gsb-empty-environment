import type { StorageAccessErrorName } from "./types.ts";

/**
 * 模拟浏览器 DOMException：requestStorageAccess 以拒绝的 Promise 形式返回，
 * 错误名与规范对齐。
 */
export class StorageAccessError extends Error {
  override readonly name: StorageAccessErrorName;

  constructor(name: StorageAccessErrorName, message: string) {
    super(message);
    this.name = name;
  }
}

export function reject<T = never>(
  name: StorageAccessErrorName,
  message: string,
): Promise<T> {
  return Promise.reject(new StorageAccessError(name, message));
}
