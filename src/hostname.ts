export function normalizeHostname(name: string): string {
  let normalized = name.trim().toLowerCase();
  if (normalized.endsWith(".")) normalized = normalized.slice(0, -1);
  return normalized;
}

export function normalizeSuffix(suffix: string): string {
  let normalized = normalizeHostname(suffix);
  if (normalized.startsWith(".")) normalized = normalized.slice(1);
  return normalized;
}
