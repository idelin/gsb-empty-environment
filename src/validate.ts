import { parseNpmrc } from './parser.js';
import type {
  NpmrcEntry,
  NpmrcIssue,
  ValidateOptions,
  ValidateResult,
} from './types.js';
import {
  buildRegisteredHosts,
  extractEnvVars,
  isForbiddenExecutableKey,
  isRegistryKey,
  parseAuthKey,
} from './util.js';

/**
 * 校验一段项目级 .npmrc 文本是否可以安全地并入包管理器配置。
 *
 * 规则：
 * 1. //host/:_authToken 等认证行的主机必须与登记 registry 主机一致；
 * 2. 值中的 ${VAR}/$VAR 只允许白名单变量，且认证/registry 条目
 *    只能发往登记主机；
 * 3. script-shell、git 等可执行路径键一律拒绝；
 * 4. 顶层与作用域 registry 指向未登记主机一律拒绝。
 */
export function validateNpmrc(text: string, options: ValidateOptions): ValidateResult {
  const { entries, issues: parseIssues } = parseNpmrc(text);
  const issues: NpmrcIssue[] = [...parseIssues];

  const registeredHosts = safeBuildHosts(options.registries);
  const allowed = new Set(options.allowedEnvVars.map((name) => name.toUpperCase()));

  for (const entry of entries) {
    checkExecutableKey(entry, issues);
    const envIssueVars = checkEnvVars(entry, allowed, issues);
    checkAuthHost(entry, registeredHosts, issues);
    checkRegistryHost(entry, registeredHosts, allowed, envIssueVars, issues);
  }

  return { ok: issues.length === 0, entries, issues };
}

function safeBuildHosts(registries: string[]): Set<string> {
  try {
    return buildRegisteredHosts(registries);
  } catch {
    throw new TypeError('registries 中包含无法解析的 URL，请检查调用方登记配置');
  }
}

function checkExecutableKey(entry: NpmrcEntry, issues: NpmrcIssue[]): void {
  if (isForbiddenExecutableKey(entry.key)) {
    issues.push({
      code: 'forbidden-executable-key',
      line: entry.line,
      key: entry.key,
      message: `项目配置不允许设置可执行路径键 ${entry.key}（第 ${entry.line} 行）`,
    });
  }
}

/** 返回命中的非白名单变量，供 registry URL 解析时避免重复报错。 */
function checkEnvVars(
  entry: NpmrcEntry,
  allowed: Set<string>,
  issues: NpmrcIssue[],
): string[] {
  const blocked: string[] = [];
  for (const variable of extractEnvVars(entry.value)) {
    if (!allowed.has(variable.toUpperCase())) {
      blocked.push(variable);
      issues.push({
        code: 'non-whitelisted-env-var',
        line: entry.line,
        key: entry.key,
        message: `第 ${entry.line} 行引用了未在白名单中的环境变量 ${variable}`,
        detail: { variable },
      });
    }
  }
  return blocked;
}

function checkAuthHost(
  entry: NpmrcEntry,
  registeredHosts: Set<string>,
  issues: NpmrcIssue[],
): void {
  const auth = parseAuthKey(entry.key);
  if (!auth) {
    return;
  }
  if (!registeredHosts.has(auth.host)) {
    issues.push({
      code: 'unregistered-auth-host',
      line: entry.line,
      key: entry.key,
      message:
        `第 ${entry.line} 行认证信息指向未登记主机 ${auth.host}，` +
        '令牌不得发往登记 registry 之外的主机',
      detail: { host: auth.host },
    });
  }
}

function checkRegistryHost(
  entry: NpmrcEntry,
  registeredHosts: Set<string>,
  allowedEnvVars: Set<string>,
  blockedEnvVars: string[],
  issues: NpmrcIssue[],
): void {
  if (!isRegistryKey(entry.key)) {
    return;
  }
  const candidate = expandAllowedForParse(entry.value, allowedEnvVars);
  let host: string;
  try {
    host = new URL(candidate).host.toLowerCase();
  } catch {
    if (blockedEnvVars.length === 0) {
      issues.push({
        code: 'invalid-url',
        line: entry.line,
        key: entry.key,
        message: `第 ${entry.line} 行 registry 值不是合法 URL：${entry.value}`,
      });
    }
    return;
  }
  if (!registeredHosts.has(host)) {
    issues.push({
      code: 'unregistered-registry-host',
      line: entry.line,
      key: entry.key,
      message:
        `第 ${entry.line} 行 ${entry.key} 指向未登记主机 ${host}，` +
        'registry 必须由调用方预先登记',
      detail: { host },
    });
  }
}

/**
 * 仅为 URL 解析临时展开白名单变量；非白名单引用原样保留，
 * 对应条目已经由环境变量规则拒绝。
 */
function expandAllowedForParse(value: string, allowed: Set<string>): string {
  return value.replace(
    /\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)/g,
    (whole, braced: string | undefined, bare: string | undefined) => {
      const name = braced ?? bare;
      return allowed.has(name.toUpperCase()) ? process.env[name] ?? whole : whole;
    },
  );
}
