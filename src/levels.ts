import type {
  LevelInput,
  MaskingStrategy,
  SensitivityLevel,
} from "./types.ts";

export const LEVEL_RANK: Record<SensitivityLevel, number> = {
  public: 0,
  internal: 1,
  sensitive: 2,
  restricted: 3,
};

const LEVEL_ALIASES: Record<string, SensitivityLevel> = {
  public: "public",
  none: "public",
  "0": "public",
  internal: "internal",
  low: "internal",
  "1": "internal",
  sensitive: "sensitive",
  medium: "sensitive",
  confidential: "sensitive",
  private: "sensitive",
  protected: "sensitive",
  pii: "sensitive",
  "2": "sensitive",
  restricted: "restricted",
  high: "restricted",
  secret: "restricted",
  "top-secret": "restricted",
  topsecret: "restricted",
  critical: "restricted",
  "3": "restricted",
};

export function normalizeLevel(input: LevelInput): SensitivityLevel {
  const key = String(input).toLowerCase();
  const level = LEVEL_ALIASES[key];
  if (!level) {
    throw new TypeError(`Unknown sensitivity level: ${String(input)}`);
  }
  return level;
}

export function maxLevel(
  left: SensitivityLevel,
  right: SensitivityLevel,
): SensitivityLevel {
  return LEVEL_RANK[left] >= LEVEL_RANK[right] ? left : right;
}

export function isPublic(level: SensitivityLevel): boolean {
  return level === "public";
}

export const DEFAULT_LEVEL_STRATEGIES: Record<
  SensitivityLevel,
  MaskingStrategy
> = {
  public: "tokenize",
  internal: "tokenize",
  sensitive: "tokenize",
  restricted: "tokenize",
};
