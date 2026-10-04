/** 版本号：源节点的 (epoch, 序号) 二元组，字典序比较。 */
export interface Version {
  epoch: number;
  seq: number;
}

export function compareVersion(a: Version, b: Version): number {
  if (a.epoch !== b.epoch) return a.epoch < b.epoch ? -1 : 1;
  if (a.seq !== b.seq) return a.seq < b.seq ? -1 : 1;
  return 0;
}

export function formatVersion(v: Version): string {
  return `(${v.epoch},${v.seq})`;
}
