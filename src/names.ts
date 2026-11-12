/**
 * Java 二进制名解析与放行匹配。
 *
 * 攻击面：来宾脚本通过 Java.type 拿到的名字可能是
 *   - 内部类二进制名（Outer$Inner），$ 之后的名字不能绕过外部类的放行；
 *   - 数组类型描述符（[Lcom.acme.Foo;、[[I 等），放行判断只看元素类型；
 *   - 同前缀的另一个包（com.acme.api2 冒充 com.acme.api），
 *     放行项必须以包边界（'.'）结尾才允许前缀匹配。
 */

/** 永不放行的宿主包前缀，即使放行清单里显式列出也拒绝。 */
export const FORBIDDEN_PACKAGE_PREFIXES: readonly string[] = [
  'java.lang.reflect.',
  'java.lang.invoke.',
  'sun.',
];

export type ClassNameResult =
  | { ok: true; canonical: string }
  | { ok: false; reason: string };

const IDENTIFIER = /^(?:[A-Za-z_$][A-Za-z0-9_$]*)$/;

/**
 * 把 Java.type 输入的类名归一化为外部类的二进制名。
 * 数组剥离所有 '[' 前缀并解码元素类型；内部类截断第一个 '$'。
 */
export function resolveHostClassName(rawName: string): ClassNameResult {
  if (typeof rawName !== 'string' || rawName.length === 0) {
    return { ok: false, reason: '类名必须是非空字符串' };
  }

  let name = rawName;

  // 数组类型：描述符以一个或多个 '[' 开头（如 [Lcom.acme.Foo;、[[I）。
  let dimensions = 0;
  while (name.startsWith('[')) {
    dimensions += 1;
    name = name.slice(1);
  }
  if (dimensions > 255) {
    return { ok: false, reason: `数组维度超出 JVM 限制: ${rawName}` };
  }

  if (dimensions > 0) {
    if (name.startsWith('L') && name.endsWith(';')) {
      name = name.slice(1, -1);
    } else {
      // 基本类型数组（[I、[[Z 等）不引用任何宿主类，无宿主类可访问。
      if (isPrimitiveDescriptor(name)) {
        return { ok: false, reason: `基本类型数组无可访问的宿主类: ${rawName}` };
      }
      return { ok: false, reason: `非法数组类型描述符: ${rawName}` };
    }
  }

  if (name.length === 0) {
    return { ok: false, reason: `非法类名: ${rawName}` };
  }

  // 内部类：放行判断只看外层类，首个 '$' 之后全部截断。
  const dollarIndex = name.indexOf('$');
  if (dollarIndex === 0) {
    return { ok: false, reason: `非法内部类名: ${rawName}` };
  }
  const outerName = dollarIndex === -1 ? name : name.slice(0, dollarIndex);

  // 默认包下的类没有 '.'，按单个标识符处理；否则逐段校验。
  const segments = outerName.split('.');
  for (const segment of segments) {
    if (!IDENTIFIER.test(segment)) {
      return { ok: false, reason: `非法 Java 二进制名: ${rawName}` };
    }
  }

  return { ok: true, canonical: outerName };
}

function isPrimitiveDescriptor(descriptor: string): boolean {
  return descriptor === 'Z' ||
    descriptor === 'B' ||
    descriptor === 'C' ||
    descriptor === 'S' ||
    descriptor === 'I' ||
    descriptor === 'J' ||
    descriptor === 'F' ||
    descriptor === 'D';
}

/**
 * 判断归一化后的类名是否命中永不放行的包。
 */
export function isForbiddenClass(canonicalName: string): boolean {
  return FORBIDDEN_PACKAGE_PREFIXES.some((prefix) =>
    canonicalName.startsWith(prefix),
  );
}

/**
 * 按放行清单判断单个归一化类名。默认拒绝，且禁止包优先于清单。
 *
 * 放行项两种形态：
 *   - 以 '.' 结尾：包前缀，仅匹配该包及其子包（按 '.' 边界）；
 *   - 不以 '.' 结尾：精确类名匹配。
 */
export function matchesAllowList(
  canonicalName: string,
  allowList: readonly string[],
): boolean {
  if (isForbiddenClass(canonicalName)) {
    return false;
  }

  return allowList.some((entry) => {
    if (entry.endsWith('.')) {
      // 包前缀必须落在包边界上：com.acme.api. 不匹配 com.acme.api2.Foo。
      return canonicalName.startsWith(entry);
    }
    return canonicalName === entry;
  });
}

/**
 * 校验放行清单条目本身：拒绝空串、非法二进制名、禁止包条目，
 * 并拒绝不带结尾 '.' 的包名形态（避免把包名误当精确类名配置）。
 */
export function validateAllowListEntry(entry: unknown): string | null {
  if (typeof entry !== 'string' || entry.length === 0) {
    return '放行清单条目必须是非空字符串';
  }
  if (entry.includes('[')) {
    return `放行清单条目不能使用数组描述符，请配置元素类型: ${entry}`;
  }
  if (entry.includes('$')) {
    return `放行清单条目不能使用内部类名，请配置外部类: ${entry}`;
  }

  const isPackagePrefix = entry.endsWith('.');
  const body = isPackagePrefix ? entry.slice(0, -1) : entry;
  if (body.length === 0) {
    return `非法放行清单条目: ${entry}`;
  }

  const segments = body.split('.');
  for (const segment of segments) {
    if (!IDENTIFIER.test(segment)) {
      return `非法 Java 二进制名: ${entry}`;
    }
  }

  const dottedEntry = isPackagePrefix ? entry : `${entry}.`;
  if (FORBIDDEN_PACKAGE_PREFIXES.some((prefix) => dottedEntry.startsWith(prefix))) {
    return `禁止包永不放行，不能加入清单: ${entry}`;
  }

  return null;
}

/**
 * 供上下文创建前的宿主类查询闸门：解析原始类名并按清单裁决。
 */
export function isHostClassAllowed(
  rawName: string,
  allowList: readonly string[],
): boolean {
  const resolved = resolveHostClassName(rawName);
  if (!resolved.ok) {
    return false;
  }
  return matchesAllowList(resolved.canonical, allowList);
}
