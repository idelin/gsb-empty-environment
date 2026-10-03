export type NodeId = string;

export interface LsaLink {
  neighbor: NodeId;
  cost: number;
}

export interface LSA {
  originator: NodeId;
  seq: number;
  links: LsaLink[];
}

export interface FibEntry {
  nextHop: NodeId;
  cost: number;
}

export type Adjacency = Map<NodeId, Map<NodeId, number>>;
