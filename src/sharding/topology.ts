export interface NodeInfo {
  id: string;
  region: string;
  zone: string;
  capacity: number;
}

export interface Location {
  region: string;
  zone: string;
}

export const COST_SAME_ZONE = 1;
export const COST_SAME_REGION = 5;
export const COST_CROSS_REGION = 20;

export function localityCost(from: Location, to: NodeInfo): number {
  if (from.region !== to.region) return COST_CROSS_REGION;
  return from.zone === to.zone ? COST_SAME_ZONE : COST_SAME_REGION;
}

export function zoneKey(node: Pick<NodeInfo, 'region' | 'zone'>): string {
  return `${node.region}/${node.zone}`;
}
