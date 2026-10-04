import test from "node:test";
import assert from "node:assert/strict";
import { isMajority } from "./config.ts";
import { Cluster, sameEntry } from "./cluster.ts";

const OLD = ["A", "B", "C"];
const NEW = ["D", "E", "F"];

function clusterWithBothGroups(): Cluster {
  const cluster = new Cluster(OLD);
  for (const id of NEW) cluster.addNode(id);
  return cluster;
}

test("稳定期：普通日志按单一多数组规则提交", () => {
  const cluster = new Cluster(OLD);
  cluster.propose("set x = 1");

  cluster.advanceCommitIndex(); // 仅领导者自己持有：1/3 不足多数
  assert.equal(cluster.leader().commitIndex, 0);

  cluster.replicateTo(["B"]); // A,B 持有：2/3 达到旧组多数
  cluster.advanceCommitIndex();
  assert.equal(cluster.leader().commitIndex, 1);
});

test("联合期：仅旧组多数不能提交，必须新旧两组各自达到多数", () => {
  const cluster = clusterWithBothGroups();
  cluster.proposeConfig({ kind: "enter-joint", newMembers: NEW });

  // 旧组多数 {A,B} 持有，新组 0 副本 —— 不能提交
  cluster.replicateTo(["B"]);
  cluster.advanceCommitIndex();
  assert.equal(cluster.leader().commitIndex, 0);
  assert.equal(cluster.leader().committedConfig.kind, "stable");

  // 新组多数 {D,E} 也持有后 —— 双多数满足，配置项提交，进入联合期
  cluster.replicateTo(["D", "E"]);
  cluster.advanceCommitIndex();
  assert.equal(cluster.leader().commitIndex, 1);
  assert.deepEqual(cluster.leader().committedConfig, {
    kind: "joint",
    oldMembers: OLD,
    newMembers: NEW,
  });

  // 联合期追加的普通日志同样要求双多数
  cluster.propose("set y = 2");
  cluster.replicateTo(["B"]); // 仅旧组多数
  cluster.advanceCommitIndex();
  assert.equal(cluster.leader().commitIndex, 1);
  cluster.replicateTo(["D", "E"]); // 补齐新组多数
  cluster.advanceCommitIndex();
  assert.equal(cluster.leader().commitIndex, 2);
});

test("配置状态机：stable -> joint -> stable，拒绝一切跳步", () => {
  const cluster = clusterWithBothGroups();

  // stable 状态下不允许 leave-joint
  assert.throws(
    () => cluster.proposeConfig({ kind: "leave-joint" }),
    /leave-joint 只能在 joint 状态下发起/,
  );
  // 禁止绕过联合确认的直接切换
  assert.throws(
    () => cluster.proposeConfig({ kind: "switch-direct", newMembers: NEW }),
    /禁止跳过联合确认/,
  );

  cluster.proposeConfig({ kind: "enter-joint", newMembers: NEW });
  // 上一个配置变更未提交，不允许再发起新的配置变更
  assert.throws(
    () => cluster.proposeConfig({ kind: "enter-joint", newMembers: NEW }),
    /存在未提交的配置变更/,
  );

  cluster.replicateTo(["B", "D", "E"]);
  cluster.advanceCommitIndex();
  assert.equal(cluster.leader().committedConfig.kind, "joint");

  // joint 状态下不允许再次 enter-joint
  assert.throws(
    () => cluster.proposeConfig({ kind: "enter-joint", newMembers: NEW }),
    /enter-joint 只能在 stable 状态下发起/,
  );
});

test("完整切换：配置项按各阶段规则提交，切换后按新组规则提交", () => {
  const cluster = clusterWithBothGroups();

  // 阶段一：C_old,new 需新旧双多数提交
  cluster.proposeConfig({ kind: "enter-joint", newMembers: NEW });
  cluster.replicateTo(["B", "D", "E"]);
  cluster.advanceCommitIndex();
  assert.equal(cluster.leader().committedConfig.kind, "joint");

  // 阶段二：C_new 在联合配置下追加，仍须双多数提交
  cluster.proposeConfig({ kind: "leave-joint" });
  cluster.replicateTo(["B"]); // 仅旧组多数 —— 不够
  cluster.advanceCommitIndex();
  assert.equal(cluster.leader().committedConfig.kind, "joint");
  cluster.replicateTo(["D", "E"]); // 补齐新组多数
  cluster.advanceCommitIndex();
  assert.deepEqual(cluster.leader().committedConfig, {
    kind: "stable",
    members: NEW,
  });

  // 切换完成后：普通日志只要求新组多数
  cluster.propose("set z = 3");
  cluster.replicateTo(["B", "C"]); // 旧组多数持有，但新组只有 A —— 不能提交
  cluster.advanceCommitIndex();
  assert.equal(cluster.leader().commitIndex, 2);
  cluster.replicateTo(["D", "E"]); // 新组多数持有 —— 提交
  cluster.advanceCommitIndex();
  assert.equal(cluster.leader().commitIndex, 3);
});

test("反例：跳过中间确认（旧组达多数、新组不能）导致已提交配置丢失", () => {
  const cluster = clusterWithBothGroups();

  // 错误实现：直接把配置切到新组，并按旧组规则提交。
  // 旧组多数 {A,B} 即可“提交”，新组 {D,E,F} 一个副本都没有。
  const committed = cluster.unsafeForceDirectSwitch(NEW);
  cluster.replicateTo(["B"]);
  cluster.advanceCommitIndex();

  // 旧组视角：配置项已提交，集群配置已是 {D,E,F}
  assert.equal(cluster.leader().commitIndex, 1);
  assert.deepEqual(cluster.leader().committedConfig, {
    kind: "stable",
    members: NEW,
  });

  // 但新组的任何多数派都与 {A,B} 不相交（此处为全不相交接替），
  // 新组多数派对这条“已提交”的配置项一无所知：
  for (const id of NEW) {
    assert.equal(cluster.nodes.get(id)!.log.length, 0);
  }

  // 于是新组可以自行选出领导者 D（新组多数票），其日志中不含已提交配置项
  assert.ok(isMajority(NEW, new Set(NEW)));
  cluster.term = 2;
  cluster.leaderId = "D";
  const newLeader = cluster.leader();
  assert.ok(!newLeader.log.some((e) => sameEntry(e, committed)));

  // 安全性破坏：A 认为配置已切到 {D,E,F} 并已提交，
  // 而 {D,E,F} 的多数派选出的领导者 D 对此毫无记录 ——
  // 两个任期里同时存在互不认可的“合法”领导者，已提交配置可能丢失。
  assert.equal(cluster.nodes.get("A")!.committedConfig.kind, "stable");
  assert.deepEqual(
    (cluster.nodes.get("A")!.committedConfig as { members: string[] })
      .members,
    NEW,
  );
  assert.equal(newLeader.activeConfig.kind, "stable");
  assert.deepEqual(
    (newLeader.activeConfig as { members: string[] }).members,
    OLD,
  );
});

test("联合共识阻止反例：旧组达多数而新组不能时，切换无法提交、无法跳步", () => {
  const cluster = clusterWithBothGroups();

  // 阶段一：C_old,new 只复制到旧组多数 {A,B}，新组 0 副本
  cluster.proposeConfig({ kind: "enter-joint", newMembers: NEW });
  cluster.replicateTo(["B"]);
  cluster.advanceCommitIndex();

  // 提交判定要求新旧双多数 —— 不能提交，已提交配置仍停留在旧组
  assert.equal(cluster.leader().commitIndex, 0);
  assert.deepEqual(cluster.leader().committedConfig, {
    kind: "stable",
    members: OLD,
  });

  // 中间确认未完成，状态机拒绝直接进入第二阶段
  assert.throws(
    () => cluster.proposeConfig({ kind: "leave-joint" }),
    /存在未提交的配置变更/,
  );

  // 补齐新组多数后 C_old,new 才提交，进入联合期
  cluster.replicateTo(["D", "E"]);
  cluster.advanceCommitIndex();
  assert.equal(cluster.leader().committedConfig.kind, "joint");

  // 阶段二：C_new 在联合规则下同样不能只靠旧组多数提交
  cluster.proposeConfig({ kind: "leave-joint" });
  cluster.replicateTo(["B"]); // 旧组多数 {A,B}，新组无副本
  cluster.advanceCommitIndex();
  assert.equal(cluster.leader().commitIndex, 1);
  assert.equal(cluster.leader().committedConfig.kind, "joint");

  // 结论：任何阶段都无法跳过“新旧两组多数确认”，
  // 新组多数派必然包含已提交的配置项，反例中的分裂被排除。
});
