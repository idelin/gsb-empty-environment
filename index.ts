export interface MergePolicy {
  /** 调用方登记的 registry 主机（hostname 或 host:port，大小写不敏感）。 */
  registeredHosts: readonly string[];
  /** 允许在项目配置中展开的环境变量名白名单。 */
  allowedEnvVars: readonly string[];
}

export type IssueCode =
  | "forbidden-executable-key"
  | "unregistered-registry-host"
  | "unregistered-auth-host"
  | "disallowed-env-var"
  | "misplaced-env-expansion";

export interface ConfigIssue {
  code: IssueCode;
  line: number;
  key: string;
  message: string;
}

export interface MergeDecision {
  allowed: boolean;
  issues: ConfigIssue[];
}

/** 可执行路径键：项目文件一律不允许设置。 */
const EXECUTABLE_PATH_KEYS: ReadonlySet<string> = new Set([
  "script-shell",
  "git",
  "shell",
]);

const ENV_REF_PATTERN = /\$\{([^}]*)\}/g;
const ENV_NAME_PATTERN = /^[A-Za-z_][A-Za-z0-9_]*$/;

interface ParsedLine {
  line: number;
  key: string;
  value: string;
}

export function parseNpmrc(source: string): ParsedLine[] {
  const out: ParsedLine[] = [];
  const lines = source.split(/\r?\n/);
  for (let i = 0; i < lines.length; i++) {
    const raw = lines[i].trim();
    if (raw === "" || raw.startsWith("#") || raw.startsWith(";")) continue;
    const eq = raw.indexOf("=");
    if (eq === -1) continue;
    const key = raw.slice(0, eq).trim();
    const value = raw.slice(eq + 1).trim();
    if (key === "") continue;
    out.push({ line: i + 1, key, value });
  }
  return out;
}

function normalizeHost(raw: string): string {
  let host = raw.trim().toLowerCase();
  if (host.includes("://")) {
    try {
      host = new URL(host).host;
    } catch {
      return "";
    }
  }
  return host.replace(/\.+$/, "");
}

function hostFromUrl(value: string): string | null {
  try {
    const host = new URL(value).host;
    return host === "" ? null : host.toLowerCase();
  } catch {
    return null;
  }
}

function envRefs(value: string): string[] {
  const refs: string[] = [];
  for (const match of value.matchAll(ENV_REF_PATTERN)) {
    refs.push(match[1]);
  }
  return refs;
}

/** `//host/path/:_authToken` 这类凭据键，返回主机部分；非凭据键返回 null。 */
function authKeyHost(key: string): string | null {
  if (!key.startsWith("//")) return null;
  const sep = key.indexOf("/:");
  if (sep === -1) return null;
  return normalizeHost(key.slice(2, sep));
}

export function evaluateProjectConfig(
  projectConfig: string,
  policy: MergePolicy,
): MergeDecision {
  const registered = new Set(
    policy.registeredHosts.map((h) => normalizeHost(h)).filter((h) => h !== ""),
  );
  const allowedVars = new Set(policy.allowedEnvVars);
  const issues: ConfigIssue[] = [];

  for (const { line, key, value } of parseNpmrc(projectConfig)) {
    const lowerKey = key.toLowerCase();

    if (EXECUTABLE_PATH_KEYS.has(lowerKey)) {
      issues.push({
        code: "forbidden-executable-key",
        line,
        key,
        message: `项目文件不允许设置可执行路径键 "${key}"`,
      });
      continue;
    }

    const authHost = authKeyHost(key);
    if (authHost !== null) {
      if (!registered.has(authHost)) {
        issues.push({
          code: "unregistered-auth-host",
          line,
          key,
          message: `凭据将发往未登记主机 "${authHost}"`,
        });
        continue;
      }
      for (const ref of envRefs(value)) {
        if (!ENV_NAME_PATTERN.test(ref) || !allowedVars.has(ref)) {
          issues.push({
            code: "disallowed-env-var",
            line,
            key,
            message: `环境变量 "${ref}" 不在白名单内`,
          });
        }
      }
      continue;
    }

    if (lowerKey === "registry" || lowerKey.endsWith(":registry")) {
      if (envRefs(value).length > 0) {
        issues.push({
          code: "misplaced-env-expansion",
          line,
          key,
          message: "registry 地址不允许包含环境变量展开",
        });
        continue;
      }
      const host = hostFromUrl(value);
      if (host === null || !registered.has(host)) {
        issues.push({
          code: "unregistered-registry-host",
          line,
          key,
          message: `registry 指向未登记主机 "${host ?? value}"`,
        });
      }
      continue;
    }

    if (envRefs(value).length > 0) {
      issues.push({
        code: "misplaced-env-expansion",
        line,
        key,
        message: "环境变量展开仅允许用于发往登记主机的凭据行",
      });
    }
  }

  return { allowed: issues.length === 0, issues };
}
