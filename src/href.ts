import { XIncludeSecurityError } from "./errors.ts";

/**
 * 将 XInclude href 归一化为“包根相对”的资源键。
 *
 * 安全约束（任一违反都抛 XIncludeSecurityError，且不允许 fallback 吞掉）：
 * - 只接受相对引用：拒绝绝对路径（/etc/passwd、C:\...、//host/share）；
 * - 拒绝任何 URI scheme（file:、http:、jar: 等），冒号仅允许出现在合法段名内时
 *   也按“可能的 scheme”从严拒绝，避免不同解析器理解差异；
 * - 拒绝反斜杠、百分号编码（%2e%2e 等）与片段标识符；
 * - 点段（. / ..）解析后不得越出包根；
 * - 完全不读取 xml:base：基准目录永远是当前文档自身在包内的目录。
 */

export function normalizeHref(
  href: string,
  baseResource: string,
): string {
  if (href.length === 0 || !href.trim()) {
    throw new XIncludeSecurityError("XInclude href 为空");
  }
  if (/[\u0000-\u001f]/.test(href)) {
    throw new XIncludeSecurityError(`href 包含控制字符: ${href}`);
  }
  if (href.includes("\\")) {
    throw new XIncludeSecurityError(`href 不允许使用反斜杠: ${href}`);
  }
  if (href.includes("%")) {
    throw new XIncludeSecurityError(`href 不允许百分号编码: ${href}`);
  }
  if (href.includes("#")) {
    throw new XIncludeSecurityError(`href 不允许片段标识符: ${href}`);
  }
  if (href.startsWith("/")) {
    throw new XIncludeSecurityError(`href 必须是包内相对路径: ${href}`);
  }
  if (hasScheme(href)) {
    throw new XIncludeSecurityError(`href 不允许绝对 URI/scheme: ${href}`);
  }

  const baseSegments = splitSegments(baseResource).slice(0, -1);
  const combined = [...baseSegments, ...splitSegments(href)];
  const resolved: string[] = [];

  for (const segment of combined) {
    if (segment === "" || segment === ".") {
      continue;
    }
    if (segment === "..") {
      if (resolved.length === 0) {
        throw new XIncludeSecurityError(
          `href 越出资源包根目录: ${href}`,
        );
      }
      resolved.pop();
      continue;
    }
    resolved.push(segment);
  }

  if (resolved.length === 0) {
    throw new XIncludeSecurityError(`href 未指向具体资源: ${href}`);
  }
  return resolved.join("/");
}

function splitSegments(reference: string): string[] {
  return reference.split("/");
}

function hasScheme(reference: string): boolean {
  // RFC 3986 scheme = ALPHA *( ALPHA / DIGIT / "+" / "-" / "." ) ":"
  const match = /^([A-Za-z][A-Za-z0-9+.-]*):/.exec(reference);
  return match !== null;
}
