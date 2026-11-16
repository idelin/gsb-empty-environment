import { validateNpmrc } from './validate.js';
import type { MergeOptions, MergeResult } from './types.js';
import { expandValue } from './util.js';

/**
 * 把项目级 .npmrc 安全合并进调用方提供的基础配置。
 *
 * 任何一条规则不通过时，项目配置整体被丢弃，只返回 base，
 * 保证恶意条目无法部分生效。通过时，项目条目覆盖同名 base 键，
 * 且只有白名单环境变量会被展开。
 */
export function mergeNpmrc(
  projectNpmrc: string,
  base: Record<string, string>,
  options: MergeOptions,
): MergeResult {
  const env = options.env ?? process.env;
  const result = validateNpmrc(projectNpmrc, {
    registries: options.registries,
    allowedEnvVars: options.allowedEnvVars,
  });

  if (!result.ok) {
    return {
      ok: false,
      config: { ...base },
      issues: result.issues,
    };
  }

  const config: Record<string, string> = { ...base };
  for (const entry of result.entries) {
    config[entry.key] = expandValue(entry.value, options.allowedEnvVars, env);
  }

  return { ok: true, config, issues: [] };
}
