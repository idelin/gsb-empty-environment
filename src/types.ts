/** 无向边（通道图上的一段） */
export interface Edge {
  from: string;
  to: string;
  dist: number;
}

/** 通道图：节点为货位/交叉口/出库点（depot） */
export interface WarehouseGraph {
  nodes: string[];
  edges: Edge[];
  depot: string;
}

/** 拣货项：某订单在某货位需要拣的数量（同一订单同一货位多行可合并后传入） */
export interface PickItem {
  id: string;
  orderId: string;
  location: string;
  qty: number;
}

export interface PlannerParams {
  /** 车容量（每趟最多装载的件数） */
  capacity: number;
  /** 拆单成本：一个订单每多用一趟车收取一次 */
  splitCost: number;
  /** 拣货项上限（精确求解复杂度 O(3^n)，默认 12） */
  maxItems?: number;
}

/** 某趟在某货位的停靠：区分各订单的拣货数量 */
export interface PickStop {
  location: string;
  picks: { orderId: string; qty: number }[];
}

export interface Trip {
  index: number;
  /** 按访问顺序的停靠点及分订单拣货量 */
  stops: PickStop[];
  /** 完整行走路径（节点序列，从 depot 出发回到 depot） */
  route: string[];
  distance: number;
  load: number;
  orderIds: string[];
}

export interface Plan {
  trips: Trip[];
  walkingCost: number;
  splitCostTotal: number;
  totalCost: number;
}
