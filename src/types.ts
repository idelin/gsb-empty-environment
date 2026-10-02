/** 仲裁拓扑模式：hierarchical=完整分级拓扑；degraded=主中心失联后的降级拓扑 */
export type TopologyMode = "hierarchical" | "degraded";

export interface RegionConfig {
  /** 区域标识，如 "IDC-A" */
  id: string;
  /** 区域在全局仲裁中的权重 */
  weight: number;
  /** 区域内节点总数 */
  localNodes: number;
  /** 区域投出有效票所需的本地多数派门限（区域内赞成节点数下限） */
  localMajorityThreshold: number;
  /** 是否为主中心 */
  isPrimary?: boolean;
}

/** 待签名的选票内容（规范化后参与签名） */
export interface Ballot {
  epoch: number;
  decision: string;
  topology: TopologyMode;
  voter: string;
}

/** 单个区域的签名投票 */
export interface SignedVote {
  regionId: string;
  weight: number;
  /** 对规范化 Ballot 的 Ed25519 签名（hex） */
  signature: string;
  /** 投票区域的公钥（PEM），供第三方独立验证 */
  publicKey: string;
}

/** 仲裁结果，携带完整签名证据 */
export interface ArbitrationResult {
  epoch: number;
  decision: string;
  topology: TopologyMode;
  /** 实际达成的权重 */
  quorumWeight: number;
  /** 当前拓扑下要求的多数派权重 */
  requiredWeight: number;
  /** 参与投票区域的签名证据 */
  votes: SignedVote[];
  /** 全部证据的 SHA-256 摘要（hex） */
  evidenceHash: string;
  decidedAt: string;
}

/** 主中心恢复时出示的接管证据 */
export interface TakeoverEvidence {
  regionId: string;
  /** 出示的纪元编号，必须严格大于当前纪元 */
  presentedEpoch: number;
  signature: string;
  publicKey: string;
}
