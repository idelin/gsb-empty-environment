import { StorageAccessError } from "./errors.ts";
import type { SandboxTokenSet } from "./types.ts";

export const ALLOW_STORAGE_ACCESS_BY_USER_ACTIVATION =
  "allow-storage-access-by-user-activation";

/**
 * sandbox 令牌检查。
 *
 * 文档处于 sandbox 且缺少 allow-storage-access-by-user-activation 令牌时，
 * requestStorageAccess 必须以 SecurityError 拒绝（规范 step 3）。
 * tokens 为 null 表示文档未被 sandbox。
 */
export function assertSandboxToken(tokens: SandboxTokenSet | null): void {
  if (tokens === null) {
    return;
  }
  if (!tokens.has(ALLOW_STORAGE_ACCESS_BY_USER_ACTIVATION)) {
    throw new StorageAccessError(
      "SecurityError",
      "sandboxed document is missing the " +
        "allow-storage-access-by-user-activation token",
    );
  }
}
