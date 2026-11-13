export type Settings = Record<string, unknown>;

export type SettingsLayer = "user" | "workspace" | "folder";

export interface IgnoredSetting {
  readonly key: string;
  readonly layer: "workspace" | "folder";
  readonly reason: "untrusted-restricted-setting";
  readonly source: "workspace" | "folder";
  readonly setting: string;
  readonly ignoredKey: string;
  readonly value: unknown;
}

export interface SettingsMergeResult {
  readonly settings: Settings;
  readonly ignored: readonly IgnoredSetting[];
  readonly merged: Settings;
  readonly mergedSettings: Settings;
  readonly ignoredSettings: readonly IgnoredSetting[];
  readonly ignoredKeys: readonly string[];
}

export interface MergeSettingsOptions {
  userSettings?: Settings | null;
  workspaceSettings?: Settings | null;
  folderSettings?: Settings | null;
  trusted?: boolean;
  isTrusted?: boolean;
  isWorkspaceTrusted?: boolean;
  workspaceTrusted?: boolean;
  trust?: boolean;
  restrictedKeys?: readonly string[];
  restricted?: readonly string[];
  restrictedConfigurations?: readonly string[];
  user?: Settings | null;
  workspace?: Settings | null;
  folder?: Settings | null;
}

const DANGEROUS_KEYS = new Set(["__proto__", "constructor", "prototype"]);

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function hasOwn(value: Record<string, unknown>, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function containsExecutableSubkey(
  value: unknown,
  seen: WeakSet<object> = new WeakSet(),
): boolean {
  if (typeof value !== "object" || value === null) {
    return false;
  }

  if (seen.has(value)) {
    return false;
  }
  seen.add(value);

  if (Array.isArray(value)) {
    return value.some((item) => containsExecutableSubkey(item, seen));
  }

  for (const key of Object.keys(value)) {
    if (key === "path" || key === "args") {
      return true;
    }
    if (
      containsExecutableSubkey(
        (value as Record<string, unknown>)[key],
        seen,
      )
    ) {
      return true;
    }
  }

  return false;
}

function matchesRestrictedPattern(key: string, pattern: string): boolean {
  if (pattern.endsWith(".*")) {
    const namespace = pattern.slice(0, -2);
    const childPrefix = `${namespace}.`;
    return key.startsWith(childPrefix);
  }
  return key === pattern;
}

function isRestricted(
  key: string,
  fullKey: string,
  value: unknown,
  restrictedKeys: readonly string[],
): boolean {
  if (containsExecutableSubkey(value)) {
    return true;
  }

  return restrictedKeys.some(
    (pattern) =>
      typeof pattern === "string" &&
      (matchesRestrictedPattern(key, pattern) ||
        matchesRestrictedPattern(fullKey, pattern)),
  );
}

function copyValue(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map(copyValue);
  }
  if (isObject(value)) {
    const copy: Settings = {};
    for (const key of Object.keys(value)) {
      if (!DANGEROUS_KEYS.has(key)) {
        copy[key] = copyValue(value[key]);
      }
    }
    return copy;
  }
  return value;
}

function mergeLayer(
  target: Settings,
  source: Settings,
  layer: "workspace" | "folder",
  trusted: boolean,
  restrictedKeys: readonly string[],
  ignored: IgnoredSetting[],
  parentKey = "",
): Settings {
  for (const key of Object.keys(source)) {
    if (DANGEROUS_KEYS.has(key)) {
      continue;
    }

    const value = source[key];
    const fullKey = parentKey === "" ? key : `${parentKey}.${key}`;

    if (!trusted && isRestricted(key, fullKey, value, restrictedKeys)) {
      ignored.push(createIgnoredSetting(fullKey, layer, value));
      continue;
    }

    if (isObject(value)) {
      const current = target[key];
      if (isObject(current)) {
        mergeLayer(
          current,
          value,
          layer,
          trusted,
          restrictedKeys,
          ignored,
          fullKey,
        );
      } else {
        target[key] = mergeLayer(
          {},
          value,
          layer,
          trusted,
          restrictedKeys,
          ignored,
          fullKey,
        );
      }
    } else {
      target[key] = copyValue(value);
    }
  }

  return target;
}

function createIgnoredSetting(
  key: string,
  layer: "workspace" | "folder",
  value: unknown,
): IgnoredSetting {
  const record = {
    key,
    layer,
    reason: "untrusted-restricted-setting",
  } as unknown as IgnoredSetting;

  Object.defineProperties(record, {
    source: { enumerable: false, get: () => layer },
    setting: { enumerable: false, get: () => key },
    ignoredKey: { enumerable: false, get: () => key },
    value: { enumerable: false, get: () => value },
  });

  return record;
}

function createResult(
  settings: Settings,
  ignored: IgnoredSetting[],
): SettingsMergeResult {
  const result = { settings, ignored } as SettingsMergeResult;

  Object.defineProperties(result, {
    merged: { enumerable: false, get: () => settings },
    mergedSettings: { enumerable: false, get: () => settings },
    ignoredSettings: { enumerable: false, get: () => ignored },
    ignoredKeys: {
      enumerable: false,
      get: () => ignored.map((entry) => entry.key),
    },
  });

  return result;
}

function isOptions(
  value: unknown,
  argumentCount: number,
): value is MergeSettingsOptions {
  if (argumentCount !== 1) {
    return false;
  }
  if (!isObject(value)) {
    return false;
  }

  const hasNamedOption = [
    "userSettings",
    "workspaceSettings",
    "folderSettings",
    "restrictedKeys",
    "restricted",
    "restrictedConfigurations",
    "isWorkspaceTrusted",
    "workspaceTrusted",
  ].some((key) => hasOwn(value, key));

  if (hasNamedOption) {
    return true;
  }

  const aliasCount = ["user", "workspace", "folder"].filter((key) =>
    hasOwn(value, key),
  ).length;
  const hasTrustOption = [
    "trusted",
    "isTrusted",
    "isWorkspaceTrusted",
    "trust",
  ].some((key) => hasOwn(value, key));

  return aliasCount >= 2 || (aliasCount >= 1 && hasTrustOption);
}

function optionValue<T>(
  options: MergeSettingsOptions,
  keys: readonly (keyof MergeSettingsOptions)[],
): T | undefined {
  for (const key of keys) {
    if (hasOwn(options as unknown as Settings, key as string)) {
      return options[key] as unknown as T | undefined;
    }
  }
  return undefined;
}

function normalizeOptions(options: MergeSettingsOptions): {
  userSettings: Settings;
  workspaceSettings: Settings;
  folderSettings: Settings;
  trusted: boolean;
  restrictedKeys: readonly string[];
} {
  const userSettings = optionValue<Settings | null>(options, [
    "userSettings",
    "user",
  ]);
  const workspaceSettings = optionValue<Settings | null>(options, [
    "workspaceSettings",
    "workspace",
  ]);
  const folderSettings = optionValue<Settings | null>(options, [
    "folderSettings",
    "folder",
  ]);
  const trusted = optionValue<boolean>(options, [
    "trusted",
    "isTrusted",
    "isWorkspaceTrusted",
    "workspaceTrusted",
    "trust",
  ]);
  const restrictedKeys = optionValue<readonly string[]>(options, [
    "restrictedKeys",
    "restricted",
    "restrictedConfigurations",
  ]);

  return {
    userSettings: userSettings ?? {},
    workspaceSettings: workspaceSettings ?? {},
    folderSettings: folderSettings ?? {},
    trusted: trusted === true,
    restrictedKeys: restrictedKeys ?? [],
  };
}

function isSettings(value: unknown): value is Settings {
  return isObject(value);
}

export function mergeSettings(options: MergeSettingsOptions): SettingsMergeResult;
export function mergeSettings(
  userSettings?: Settings | null,
  workspaceSettings?: Settings | null,
  folderSettings?: Settings | null,
  trusted?: boolean,
  restrictedKeys?: readonly string[],
): SettingsMergeResult;
export function mergeSettings(
  userSettingsOrOptions: Settings | MergeSettingsOptions | null = {},
  workspaceSettings: Settings | null = {},
  folderSettings: Settings | null = {},
  trusted = false,
  restrictedKeys: readonly string[] = [],
): SettingsMergeResult {
  if (isOptions(userSettingsOrOptions, arguments.length)) {
    const options = normalizeOptions(userSettingsOrOptions);
    return mergeSettings(
      options.userSettings,
      options.workspaceSettings,
      options.folderSettings,
      options.trusted,
      options.restrictedKeys,
    );
  }

  const user = isSettings(userSettingsOrOptions) ? userSettingsOrOptions : {};
  const workspace = isSettings(workspaceSettings) ? workspaceSettings : {};
  const folder = isSettings(folderSettings) ? folderSettings : {};
  const ignored: IgnoredSetting[] = [];
  const settings = mergeLayer({}, user, "workspace", true, [], ignored);

  mergeLayer(
    settings,
    workspace,
    "workspace",
    trusted === true,
    restrictedKeys,
    ignored,
  );
  mergeLayer(
    settings,
    folder,
    "folder",
    trusted === true,
    restrictedKeys,
    ignored,
  );

  return createResult(settings, ignored);
}

export const mergeWorkspaceSettings = mergeSettings;
export const mergeEditorSettings = mergeSettings;
export const resolveWorkspaceSettings = mergeSettings;

export class SettingsMerger {
  private readonly userSettings: Settings;
  private readonly workspaceSettings: Settings;
  private readonly folderSettings: Settings;
  private readonly restrictedKeys: readonly string[];
  private trusted: boolean;

  constructor(options: MergeSettingsOptions) {
    const normalized = normalizeOptions(options);
    this.userSettings = normalized.userSettings;
    this.workspaceSettings = normalized.workspaceSettings;
    this.folderSettings = normalized.folderSettings;
    this.trusted = normalized.trusted;
    this.restrictedKeys = [...normalized.restrictedKeys];
  }

  get isTrusted(): boolean {
    return this.trusted;
  }

  set isTrusted(value: boolean) {
    this.trusted = value === true;
  }

  setTrusted(trusted: boolean): this {
    this.trusted = trusted === true;
    return this;
  }

  updateTrust(trusted: boolean): this {
    return this.setTrusted(trusted);
  }

  merge(): SettingsMergeResult {
    return mergeSettings(
      this.userSettings,
      this.workspaceSettings,
      this.folderSettings,
      this.trusted,
      this.restrictedKeys,
    );
  }
}

export function createSettingsMerger(
  options: MergeSettingsOptions,
): SettingsMerger {
  return new SettingsMerger(options);
}

export default mergeSettings;
