"""演示用有限状态模型。"""
from liveness import Model, Transition as T


def unreliable_server():
    """不可靠服务器：收到请求后可能响应，也可能永远忽略（自循环）。"""
    return Model(
        "unreliable-server",
        initial="idle",
        transitions=[
            T("idle", "request", "busy"),
            T("busy", "respond", "served"),
            T("served", "reset", "idle"),
            T("busy", "ignore", "busy"),
        ],
        labels={"idle": set(), "busy": {"req"}, "served": {"resp"}},
    )


def deaf_server():
    """聋哑服务器：收到请求后只会空转，永远没有响应动作。"""
    return Model(
        "deaf-server",
        initial="idle",
        transitions=[
            T("idle", "request", "busy"),
            T("busy", "spin", "busy"),
        ],
        labels={"idle": set(), "busy": {"req"}},
    )


def lossy_channel():
    """有损信道：请求入队后反复派发-丢失，除非最终送达。"""
    return Model(
        "lossy-channel",
        initial="idle",
        transitions=[
            T("idle", "request", "queued"),
            T("queued", "dispatch", "sending"),
            T("sending", "lost", "queued"),
            T("sending", "delivered", "done"),
            T("done", "reset", "idle"),
        ],
        labels={
            "idle": set(),
            "queued": {"req"},
            "sending": {"req"},
            "done": {"resp"},
        },
    )


def slow_responder(n=6):
    """慢响应者：请求后需 n 步才响应（用于演示截断 -> 未决）。"""
    transitions = [T("s0", "request", "s1")]
    for i in range(1, n):
        transitions.append(T(f"s{i}", "work", f"s{i+1}"))
    transitions.append(T(f"s{n}", "respond", "done"))
    transitions.append(T("done", "reset", "s0"))
    labels = {"s0": set(), "done": {"resp"}}
    for i in range(1, n + 1):
        labels[f"s{i}"] = {"req"}
    return Model("slow-responder", "s0", transitions, labels)


def unreachable_bad_loop():
    """主行为正常，但含一个不可达的坏自循环分量（不可达不算反例）。"""
    return Model(
        "unreachable-bad-loop",
        initial="idle",
        transitions=[
            T("idle", "request", "busy"),
            T("busy", "respond", "served"),
            T("served", "reset", "idle"),
            T("zombie", "spin", "zombie"),
        ],
        labels={
            "idle": set(),
            "busy": {"req"},
            "served": {"resp"},
            "zombie": {"req"},
        },
    )


def flaky_endpoint():
    """a<->b 循环空转；动作 fix 仅在 a 可执行并跳出循环（弱/强公平对比）。"""
    return Model(
        "flaky-endpoint",
        initial="boot",
        transitions=[
            T("boot", "start", "a"),
            T("a", "tick", "b"),
            T("b", "tock", "a"),
            T("a", "fix", "repaired"),
            T("repaired", "respond", "a"),
        ],
        labels={
            "boot": set(),
            "a": {"req"},
            "b": {"req"},
            "repaired": {"resp"},
        },
    )
