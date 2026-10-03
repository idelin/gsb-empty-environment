export type NodeId = string;

/** 路由器接口（本地链路）状态 */
export interface LinkInfo {
  neighbor: NodeId;
  cost: number;
  up: boolean;
}

/** 链路状态通告（Link State Advertisement） */
export interface LSA {
  origin: NodeId;
  seq: number;
  age: number;
  links: LinkInfo[];
}

/** 转发表项 */
export interface Route {
  destination: NodeId;
  nextHop: NodeId | null;
  cost: number;
}

/** 邻接表：邻居 -> 代价（仅包含 Up 且双向校验通过的链路） */
export type Adjacency = Map<NodeId, number>;
export type Topology = Map<NodeId, Adjacency>;
