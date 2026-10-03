const ENTRY_OVERHEAD_BYTES = 64;

export function estimateValueBytes(value: unknown): number {
  if (value === null || value === undefined) return 0;
  const json = JSON.stringify(value) ?? "";
  return json.length * 2;
}

export function estimateEntryBytes(key: string, value: unknown): number {
  return ENTRY_OVERHEAD_BYTES + key.length * 2 + estimateValueBytes(value);
}
