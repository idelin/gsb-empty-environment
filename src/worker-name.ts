import { sha256Hex } from './encoding.ts';

const NAME_VERSION = 'v1';
const NAME_PREFIX = 'tsw';

/**
 * 规范化脚本 URL，避免查询串顺序等无关差异导致同源同脚本派生出不同名称。
 * 解析失败时回退为原始字符串（仍参与摘要），由调用方保证 URL 合法。
 */
export function canonicalScriptUrl(scriptUrl: string): string {
  try {
    const url = new URL(scriptUrl);
    return url.origin + url.pathname + url.search;
  } catch {
    return scriptUrl;
  }
}

/**
 * 由“经认证的租户标识 + 脚本 URL”派生 SharedWorker 名称。
 * 名称是 SHA-256 摘要，调用方无法传入任意裸名称：
 * 同一脚本下不同租户必然得到不同名称，同名复用也只在同租户内生效。
 */
export async function deriveWorkerName(
  tenantId: string,
  scriptUrl: string,
): Promise<string> {
  const canonicalUrl = canonicalScriptUrl(scriptUrl);
  const digest = await sha256Hex(
    `${NAME_VERSION}\n${tenantId}\n${canonicalUrl}`,
  );
  return `${NAME_PREFIX}-${NAME_VERSION}-${digest}`;
}
