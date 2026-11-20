export interface CnameHop {
  name: string;
  ttlSec: number;
}

export interface ResolvedAddress {
  address: string;
  ttlSec: number;
}

export interface Resolution {
  chain: CnameHop[];
  addresses: ResolvedAddress[];
}

export interface Resolver {
  resolve(hostname: string): Promise<Resolution>;
}
