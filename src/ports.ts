/** 明确的端口映射规则：升级时仅按表映射，未列出的显式端口原样保留。 */
export const DEFAULT_PORT_MAP: Readonly<Record<number, number>> = Object.freeze({
  80: 443,
});

export function mapPort(
  port: number | null,
  portMap: Readonly<Record<number, number>> = DEFAULT_PORT_MAP,
): number | null {
  if (port === null) return null;
  const mapped = portMap[port];
  return mapped === undefined ? port : mapped;
}
