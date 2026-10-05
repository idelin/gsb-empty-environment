export function assert(cond: boolean, msg: string): void {
  if (!cond) throw new Error(`ASSERT FAILED: ${msg}`);
}
