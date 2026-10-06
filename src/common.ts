// Runtime pieces shared by the reference interpreter and the compiled-code machine.

export class ReturnSignal {
  value: unknown;
  constructor(value: unknown) {
    this.value = value;
  }
}

export function formatValue(value: unknown): string {
  if (value === null) return 'null';
  if (typeof value === 'object') {
    const rec = value as { kind?: string; name?: string };
    if (rec.kind === 'closure') return `<fn ${rec.name ?? 'anon'}>`;
  }
  return String(value);
}

export function truthy(value: unknown): boolean {
  return value !== false && value !== null && value !== 0;
}
