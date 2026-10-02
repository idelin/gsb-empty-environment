import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  HierarchicalArbiter,
  QuorumNotReachedError,
  StaleEpochError,
  verifyVote,
  type ArbitrationResult,
} from "../src/index.ts";

/** 验收拓扑：IDC-A 权重 2（主中心），IDC-B / IDC-C 权重各 1 */
function createArbiter(): HierarchicalArbiter {
  return new HierarchicalArbiter([
    { id: "IDC-A", weight: 2, localNodes: 3, localMajorityThreshold: 2, isPrimary: true },
    { id: "IDC-B", weight: 1, localNodes: 3, localMajorityThreshold: 2 },
    { id: "IDC-C", weight: 1, localNodes: 3, localMajorityThreshold: 2 },
  ]);
}

describe("正常分级拓扑（hierarchical）", () => {
  it("总权重 4，多数派门限为 3", () => {
    const arbiter = createArbiter();
    assert.equal(arbiter.requiredWeight(), 3);
    assert.equal(arbiter.currentTopology, "hierarchical");
  });

  it("A(2)+B(1)+C(1) 全票达成决议，证据可独立验证", () => {
    const arbiter = createArbiter();
    const result = arbiter.arbitrate("promote-config-v1");
    assert.equal(result.topology, "hierarchical");
    assert.equal(result.quorumWeight, 4);
    assert.equal(result.votes.length, 3);
    assert.ok(arbiter.verifyEvidence(result));
  });

  it("A 本地门限未达成时，B+C 权重不足（1+1<3），正常拓扑拒绝决议", () => {
    const arbiter = createArbiter();
    arbiter.region("IDC-A").localYesVotes = 0; // A 可达但本地多数派未达成，不投票
    assert.throws(
      () => arbiter.arbitrate("x"),
      (err: unknown) => err instanceof QuorumNotReachedError
        && (err as QuorumNotReachedError).achieved === 2
        && (err as QuorumNotReachedError).required === 3,
    );
  });
});

describe("降级拓扑（degraded）：主中心失联", () => {
  it("A 失联后自动降级并推进纪元，B+C 达成临时多数派", () => {
    const arbiter = createArbiter();
    const epochBefore = arbiter.currentEpoch;
    arbiter.setReachable("IDC-A", false);

    assert.equal(arbiter.currentTopology, "degraded");
    assert.equal(arbiter.currentEpoch, epochBefore + 1);
    assert.equal(arbiter.requiredWeight(), 2); // floor(2/2)+1

    const result = arbiter.arbitrate("emergency-write");
    assert.equal(result.topology, "degraded");
    assert.equal(result.quorumWeight, 2);
    assert.deepEqual(
      result.votes.map((v) => v.regionId).sort(),
      ["IDC-B", "IDC-C"],
    );
    assert.ok(arbiter.verifyEvidence(result));
  });

  it("降级拓扑下 C 本地门限未达成时，B 单独无法达成临时多数派（1<2）", () => {
    const arbiter = createArbiter();
    arbiter.setReachable("IDC-A", false);
    arbiter.region("IDC-C").localYesVotes = 0; // C 可达但本地多数派未达成
    assert.throws(() => arbiter.arbitrate("x"), QuorumNotReachedError);
  });

  it("降级拓扑门限随可达池动态重算：仅剩 B 时 B 可独立维持可用性", () => {
    const arbiter = createArbiter();
    arbiter.setReachable("IDC-A", false);
    arbiter.setReachable("IDC-C", false);
    assert.equal(arbiter.requiredWeight(), 1); // floor(1/2)+1
    const result = arbiter.arbitrate("last-region-standing");
    assert.equal(result.quorumWeight, 1);
    assert.ok(arbiter.verifyEvidence(result));
  });

  it("非主中心失联不触发降级", () => {
    const arbiter = createArbiter();
    arbiter.setReachable("IDC-C", false);
    assert.equal(arbiter.currentTopology, "hierarchical");
    // A(2)+B(1)=3 仍满足门限 3
    const result = arbiter.arbitrate("still-ok");
    assert.equal(result.quorumWeight, 3);
  });
});

describe("主中心恢复接管（纪元围栏，无状态冲突）", () => {
  it("A 出示更高纪元重新接管，旧纪元决议被拒绝应用", () => {
    const arbiter = createArbiter();
    const normalDecision = arbiter.arbitrate("before-partition");

    arbiter.setReachable("IDC-A", false);
    const degradedDecision = arbiter.arbitrate("during-partition");
    const degradedEpoch = arbiter.currentEpoch;

    // A 恢复，出示比降级纪元更高的纪元
    const takeover = arbiter.recoverPrimary(degradedEpoch + 1);
    assert.equal(takeover.regionId, "IDC-A");
    assert.equal(arbiter.currentTopology, "hierarchical");
    assert.equal(arbiter.currentEpoch, degradedEpoch + 1);

    // 接管证据签名可验证
    assert.ok(
      verifyVote(
        {
          epoch: takeover.presentedEpoch,
          decision: `takeover:IDC-A:${takeover.presentedEpoch}`,
          topology: "hierarchical",
          voter: "IDC-A",
        },
        { ...takeover, weight: 2 },
      ),
    );

    // 纪元围栏：旧纪元的决议（包括降级期的）一律拒绝应用 → 无状态冲突
    assert.equal(arbiter.applyDecision(normalDecision), false);
    assert.equal(arbiter.applyDecision(degradedDecision), false);

    // 新纪元决议正常应用
    const newDecision = arbiter.arbitrate("after-takeover");
    assert.equal(arbiter.applyDecision(newDecision), true);
  });

  it("A 出示不高于当前纪元的编号时被拒绝（防脑裂）", () => {
    const arbiter = createArbiter();
    arbiter.setReachable("IDC-A", false);
    const degradedEpoch = arbiter.currentEpoch;
    assert.throws(() => arbiter.recoverPrimary(degradedEpoch), StaleEpochError);
    assert.throws(() => arbiter.recoverPrimary(degradedEpoch - 1), StaleEpochError);
    assert.equal(arbiter.currentTopology, "degraded");
  });
});

describe("签名证据完整性", () => {
  it("篡改决议内容后证据验证失败", () => {
    const arbiter = createArbiter();
    const result = arbiter.arbitrate("transfer-100");
    const tampered: ArbitrationResult = { ...result, decision: "transfer-999" };
    assert.equal(arbiter.verifyEvidence(tampered), false);
  });

  it("篡改证据摘要后验证失败", () => {
    const arbiter = createArbiter();
    const result = arbiter.arbitrate("transfer-100");
    const tampered: ArbitrationResult = { ...result, evidenceHash: "00".repeat(32) };
    assert.equal(arbiter.verifyEvidence(tampered), false);
  });

  it("伪造投票权重后验证失败", () => {
    const arbiter = createArbiter();
    arbiter.setReachable("IDC-A", false);
    const result = arbiter.arbitrate("degraded-op");
    // 只保留 B 一票但伪造其权重为 2
    const forged: ArbitrationResult = {
      ...result,
      votes: [{ ...result.votes.find((v) => v.regionId === "IDC-B")!, weight: 2 }],
    };
    assert.equal(arbiter.verifyEvidence(forged), false); // evidenceHash 复算不一致
  });
});

describe("区域本地多数派门限", () => {
  it("本地赞成票不足门限时，该区域权重不计入仲裁", () => {
    const arbiter = createArbiter();
    arbiter.region("IDC-B").localYesVotes = 1; // 门限 2，未达成
    const result = arbiter.arbitrate("partial-local");
    // 只有 A(2)+C(1)=3 票，B 未投票
    assert.equal(result.quorumWeight, 3);
    assert.ok(!result.votes.some((v) => v.regionId === "IDC-B"));
  });

  it("本地门限未达成导致全局多数派失败", () => {
    const arbiter = createArbiter();
    arbiter.region("IDC-A").localYesVotes = 1; // 主中心本地门限未达成
    arbiter.region("IDC-C").localYesVotes = 0;
    // 只剩 B(1)，1 < 3
    assert.throws(() => arbiter.arbitrate("x"), QuorumNotReachedError);
  });
});
