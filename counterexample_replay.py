#!/usr/bin/env python3
# 由 iosim 生成的独立反例重放脚本：不依赖原项目代码，可直接执行。
# 退出码 0 表示反例在两台机器上重放成功（失败确实发生）。
import json
import sys

DATA = json.loads("""{
  "candidate": {
    "name": "candidate-bad-output",
    "states": [
      "busy",
      "idle",
      "ready"
    ],
    "initial": "idle",
    "inputs": [
      "cancel",
      "req"
    ],
    "outputs": [
      "ack",
      "data",
      "error"
    ],
    "transitions": [
      [
        "busy",
        "ack",
        "ready"
      ],
      [
        "busy",
        "cancel",
        "idle"
      ],
      [
        "busy",
        "error",
        "idle"
      ],
      [
        "idle",
        "req",
        "busy"
      ],
      [
        "ready",
        "data",
        "idle"
      ]
    ]
  },
  "spec": {
    "name": "spec-server",
    "states": [
      "busy",
      "idle",
      "ready"
    ],
    "initial": "idle",
    "inputs": [
      "cancel",
      "req"
    ],
    "outputs": [
      "ack",
      "data"
    ],
    "transitions": [
      [
        "busy",
        "ack",
        "ready"
      ],
      [
        "busy",
        "cancel",
        "idle"
      ],
      [
        "idle",
        "req",
        "busy"
      ],
      [
        "ready",
        "data",
        "idle"
      ]
    ]
  },
  "steps": [
    {
      "action": "req",
      "candidate_src": "idle",
      "candidate_dst": "busy",
      "spec_src": "idle",
      "spec_dst": "busy"
    }
  ],
  "failure": {
    "kind": "unexpected_output",
    "action": "error",
    "candidate_state": "busy",
    "spec_state": "busy"
  }
}""")


def index(machine):
    post = {}
    for src, action, dst in machine["transitions"]:
        post.setdefault((src, action), set()).add(dst)
    return post


def main():
    cand, spec = DATA["candidate"], DATA["spec"]
    cp, sp = index(cand), index(spec)
    c, s = cand["initial"], spec["initial"]
    print("重放反例: 候选机 %s vs 规范 %s" % (cand["name"], spec["name"]))
    for i, step in enumerate(DATA["steps"], 1):
        action = step["action"]
        ok = (
            step["candidate_src"] == c
            and step["spec_src"] == s
            and step["candidate_dst"] in cp.get((c, action), set())
            and step["spec_dst"] in sp.get((s, action), set())
        )
        if not ok:
            print("第 %d 步重放失败: 转移不存在" % i)
            return 1
        mark = "?" if action in cand["inputs"] else "!"
        print("  步骤 %d: (%s, %s) --%s%s--> (%s, %s)" % (
            i, c, s, action, mark, step["candidate_dst"], step["spec_dst"]))
        c, s = step["candidate_dst"], step["spec_dst"]

    failure = DATA["failure"]
    action = failure["action"]
    if failure["kind"] == "missing_input":
        ok = (action in spec["inputs"]
              and (s, action) in sp
              and (c, action) not in cp)
        msg = "候选机在状态 %s 无法接收规范要求的输入 %s?" % (c, action)
    else:
        ok = (action in cand["outputs"]
              and (c, action) in cp
              and (s, action) not in sp)
        msg = "候选机在状态 %s 发出规范未允许的输出 %s!" % (c, action)
    if not ok:
        print("终点失败条件不成立，重放失败")
        return 1
    print("终点失败确认: " + msg)
    print("反例重放成功")
    return 0


if __name__ == "__main__":
    sys.exit(main())
