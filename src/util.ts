import { URL } from 'node:url';

/** 环境变量引用：支持 ${VAR} 与 $VAR 两种 npm 展开形式。 */
const ENV_VAR_PATTERN = /\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)/g;

/**
 * 项目 .npmrc 不允许设置的可执行路径 / 命令选择类键。
 * 这些键可被用于让 npm 执行攻击者指定的二进制。
 */
export const FORBIDDEN_EXECUTABLE_KEYS: readonly string[] = [
  'script-shell',
  'git',
];

/** 形如 //registry.example.org/:_authToken 的按主机认证键。 */
const AUTH_KEY_PATTERN = /^\/\/([^/]+)\/?:([\w.-]+)$/;

/** 形如 @scope:registry 的作用域 registry 键。 */
const SCOPED_REGISTRY_PATTERN = /^@[^:]+:registry$/;

/**
 * 从 registry URL 中取出主机名（小写）。
 * 抛错表示 URL 非法，由调用方转成 invalid-url 问题。
 */
export function hostFromUrl(url: string): string {
  return new URL(url).host.toLowerCase();
}

/**
 * 从登记的 registry URL 集合构造可信主机集合（小写）。
 */
export function buildRegisteredHosts(registries: string[]): Set<string> {
  const hosts = new Set<string>();
  for (const registry of registries) {
    hosts.add(hostFromUrl(registry));
  }
  return hosts;
}

/**
 * 解析认证键 //host/:credential，返回主机与凭据子键；
 * 不是认证键时返回 null。
 */
export function parseAuthKey(key: string): { host: string; credential: string } | null {
  const match = AUTH_KEY_PATTERN.exec(key);
  if (!match) {
    return null;
  }
  const [, rawHost, credential] = match;
  return { host: rawHost.toLowerCase(), credential };
}

export function isRegistryKey(key: string): boolean {
  return key === 'registry' || SCOPED_REGISTRY_PATTERN.test(key);
}

export function isForbiddenExecutableKey(key: string): boolean {
  return FORBIDDEN_EXECUTABLE_KEYS.includes(key);
}

/**
 * 取出值中引用到的全部环境变量名（去重、保持出现顺序）。
 */
export function extractEnvVars(value: string): string[] {
  const names: string[] = [];
  for (const match of value.matchAll(ENV_VAR_PATTERN)) {
    const name = match[1] ?? match[2];
    if (name && !names.includes(name)) {
      names.push(name);
    }
  }
  return names;
}

/**
 * 仅展开白名单内的变量；未登记的变量保留原样，避免凭据在
 * 判定之外被注入到最终配置。
 */
export function expandValue(
  value: string,
  allowedEnvVars: readonly string[],
  env: Record<string, string | undefined>,
): string {
  const allowed = new Set(allowedEnvVars.map((name) => name.toUpperCase()));
  return value.replace(ENV_VAR_PATTERN, (whole, braced: string | undefined, bare: string | undefined) => {
    const name = braced ?? bare;
    if (!allowed.has(name.toUpperCase())) {
      return whole;
    }
    const replacement = env[name];
    return replacement ?? whole;
  });
}
