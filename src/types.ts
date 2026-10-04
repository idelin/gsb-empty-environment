export interface GraphEdge {
  from: string;
  to: string;
  dist: number;
}

export interface WeightedGraph {
  nodes: string[];
  edges: GraphEdge[]; // undirected
}

export interface OrderLine {
  order: string;
  location: string;
  qty: number;
}

export interface Scenario {
  depot: string;
  capacity: number;   // cart capacity per trip (units)
  splitCost: number;  // cost charged for each extra trip an order is split into
  graph: WeightedGraph;
  lines: OrderLine[]; // pick items, at most 12
}

export interface Pick {
  order: string;
  qty: number;
}

export interface Stop {
  location: string;
  picks: Pick[];
}

export interface Trip {
  index: number;
  stops: Stop[];
  route: string[]; // full node sequence, depot -> ... -> depot
  distance: number;
  load: number;
}

export interface Plan {
  trips: Trip[];
  walkingCost: number;
  splitCost: number;
  totalCost: number;
}
