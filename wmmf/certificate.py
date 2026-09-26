"""加权最大最小公平证书。

证书命题：对任一未满足（rate < cap）的需求 d，存在其路由上的一条
饱和链路 l，且 l 上所有需求的归一化速率都不超过 d 的归一化速率。
于是任何可行分配若想提高 d，必然降低 l 上某个归一化速率不更高者。

证书的构造与验证都是确定性的纯算术检查，不调用求解器。
"""

from __future__ import annotations

from .allocator import Allocation


def check_feasible(allocation: Allocation) -> list[str]:
    """可行性检查：速率非负、不超过需求上限、链路负载不超过容量。"""
    errors: list[str] = []
    for name, rate in allocation.rates.items():
        if rate < 0:
            errors.append(f"demand {name}: negative rate {rate}")
        cap = allocation.demands[name].cap
        if cap is not None and rate > cap:
            errors.append(f"demand {name}: rate {rate} exceeds cap {cap}")
    for link, load in allocation.link_loads.items():
        if load > allocation.link_caps[link]:
            errors.append(
                f"link {link}: load {load} exceeds capacity "
                f"{allocation.link_caps[link]}"
            )
    return errors


def build_certificate(allocation: Allocation) -> dict[str, str]:
    """为每个未满足需求构造瓶颈链路证书；构造失败说明分配不最优。"""
    cert: dict[str, str] = {}
    for name in allocation.rates:
        if allocation.is_satisfied(name):
            continue
        level = allocation.normalized_rate(name)
        found = None
        for link in allocation.demands[name].links:
            if allocation.link_loads[link] != allocation.link_caps[link]:
                continue
            if all(
                link not in other.links
                or allocation.normalized_rate(other_name) <= level
                for other_name, other in allocation.demands.items()
            ):
                found = link
                break
        if found is None:
            raise ValueError(
                f"demand {name}: no bottleneck-link certificate exists; "
                f"allocation is not weighted max-min fair"
            )
        cert[name] = found
    return cert


def verify_certificate(allocation: Allocation, cert: dict[str, str]) -> list[str]:
    """独立验证证书（不信任构造过程），返回错误列表，空列表表示通过。"""
    errors: list[str] = []
    for name in allocation.rates:
        if allocation.is_satisfied(name):
            if name in cert:
                errors.append(f"demand {name}: satisfied demand needs no certificate")
            continue
        link = cert.get(name)
        if link is None:
            errors.append(f"demand {name}: missing bottleneck-link certificate")
            continue
        if link not in allocation.demands[name].links:
            errors.append(f"demand {name}: link {link} is not on its route")
            continue
        if allocation.link_loads[link] != allocation.link_caps[link]:
            errors.append(f"demand {name}: link {link} is not saturated")
            continue
        level = allocation.normalized_rate(name)
        for other_name, other in allocation.demands.items():
            if link in other.links and allocation.normalized_rate(other_name) > level:
                errors.append(
                    f"demand {name}: demand {other_name} on link {link} has "
                    f"higher normalized rate"
                )
    return errors


def explain(allocation: Allocation, cert: dict[str, str], name: str) -> str:
    """把证书命题翻译成一句人话。"""
    if allocation.is_satisfied(name):
        return (
            f"需求 {name} 已达上限 {allocation.demands[name].cap}，"
            f"不再继续分配，无需证书"
        )
    link = cert[name]
    level = allocation.normalized_rate(name)
    return (
        f"需求 {name}（归一速率 {level}）受瓶颈链路 {link} 约束："
        f"{link} 已饱和（负载 {allocation.link_loads[link]} = 容量 "
        f"{allocation.link_caps[link]}），且其上所有需求的归一速率均 "
        f"<= {level}；提高 {name} 必然降低其中某个归一速率不更高者"
    )
