"""纯标准库实现的重启 GMRES(m)（Python 3.11，不依赖 NumPy/SciPy）。

设计要点
--------
- 矩阵只通过 ``matvec(x, n)`` 回调访问；维度 ``n`` 显式传入，不做隐式推断。
- 工作内存严格受重启长度 ``restart`` 控制：Arnoldi 基 ``V`` 至多
  ``restart + 1`` 个向量，上 Hessenberg 矩阵 ``H`` 为
  ``(restart + 1) x restart``，即 O(n * restart + restart**2)。
- 记录每个 Arnoldi 步的残差估计、每个重启边界的真实残差、重启周期数
  以及停止原因（converged / zero_rhs / stagnation / budget_exhausted）。
- 零右端立即返回；预算耗尽或停滞时绝不伪称收敛。
- matvec 回调返回尺寸错误、非有限值或自身抛异常时，立即抛出
  ``MatvecError`` 并指明发生位置（重启周期 / Arnoldi 步）与分量下标。
"""

from __future__ import annotations

import math
from dataclasses import dataclass, field
from typing import Callable, List, Optional, Sequence

__all__ = ["gmres", "GmresResult", "GmresError", "MatvecError"]

#: matvec 回调约定：matvec(x, n) -> 长度为 n 的序列，y = A @ x。
Matvec = Callable[[List[float], int], Sequence[float]]

REASON_CONVERGED = "converged"
REASON_ZERO_RHS = "zero_rhs"
REASON_STAGNATION = "stagnation"
REASON_BUDGET = "budget_exhausted"

#: 快乐崩溃（happy breakdown）判据的相对阈值。
_BREAKDOWN_RTOL = 1e-14


class GmresError(Exception):
    """GMRES 求解过程中的错误基类。"""


class MatvecError(GmresError):
    """matvec 回调行为非法：尺寸错误、非有限值或回调自身抛异常。"""


@dataclass
class GmresResult:
    """GMRES 求解结果。

    属性
    ----
    x : 最终近似解（长度为 n 的列表）。
    converged : 是否真正收敛（预算耗尽 / 停滞时为 False）。
    reason : 停止原因，见模块级 REASON_* 常量。
    restart_cycles : 实际执行的重启周期数。
    arnoldi_steps : Arnoldi 内层步数总和。
    matvec_count : matvec 回调调用次数（预算计量口径）。
    residual_norm : 最终残差范数（优先为真实残差，预算耗尽时为 Arnoldi 估计）。
    residual_history : 每个 Arnoldi 步的残差估计 |beta*e1 - H y|。
    restart_residuals : 初始及每个重启边界重算的真实残差范数。
    """

    x: List[float]
    converged: bool
    reason: str
    restart_cycles: int
    arnoldi_steps: int
    matvec_count: int
    residual_norm: float
    residual_history: List[float] = field(default_factory=list)
    restart_residuals: List[float] = field(default_factory=list)


def _norm(v: Sequence[float]) -> float:
    return math.hypot(*v) if v else 0.0


def _dot(a: Sequence[float], b: Sequence[float]) -> float:
    return math.fsum(x * y for x, y in zip(a, b))


def _check_input_vector(vec: Sequence[float], n: int, name: str) -> None:
    if len(vec) != n:
        raise ValueError(
            f"输入 {name} 长度 {len(vec)} 与显式尺寸 n={n} 不一致"
        )
    for i, v in enumerate(vec):
        try:
            ok = math.isfinite(v)
        except TypeError:
            raise ValueError(f"输入 {name}[{i}]={v!r} 不是数值") from None
        if not ok:
            raise ValueError(f"输入 {name}[{i}]={v!r} 不是有限数值")


def _check_matvec_output(y: Sequence[float], n: int, where: str) -> List[float]:
    try:
        length = len(y)
    except TypeError:
        raise MatvecError(
            f"{where}: matvec 返回值不可取长度（应为长度为 {n} 的序列）"
        ) from None
    if length != n:
        raise MatvecError(
            f"{where}: matvec 返回长度 {length}，与显式尺寸 n={n} 不一致"
        )
    out: List[float] = []
    for i, v in enumerate(y):
        try:
            fv = float(v)
        except (TypeError, ValueError):
            raise MatvecError(
                f"{where}: matvec 返回第 {i} 个分量 {v!r} 不是数值"
            ) from None
        if not math.isfinite(fv):
            raise MatvecError(
                f"{where}: matvec 返回第 {i} 个分量为非有限值 {v!r}"
            )
        out.append(fv)
    return out


def _givens(a: float, b: float) -> tuple[float, float]:
    """返回 (c, s) 使得 [[c, s], [-s, c]] @ [a, b] = [r, 0]。"""
    if b == 0.0:
        return 1.0, 0.0
    if abs(b) > abs(a):
        t = a / b
        s = 1.0 / math.sqrt(1.0 + t * t)
        return t * s, s
    t = b / a
    c = 1.0 / math.sqrt(1.0 + t * t)
    return c, t * c


def gmres(
    matvec: Matvec,
    b: Sequence[float],
    n: int,
    *,
    x0: Optional[Sequence[float]] = None,
    restart: int = 20,
    tol: float = 1e-8,
    max_restarts: int = 100,
    max_matvec: Optional[int] = None,
    stagnation_rtol: Optional[float] = 1e-10,
) -> GmresResult:
    """用重启 GMRES(m) 求解 A x = b。

    参数
    ----
    matvec : 矩阵向量回调，签名为 matvec(x, n) -> y（y = A @ x）。
    b : 右端向量，长度必须等于显式尺寸 n。
    n : 问题维度（显式输入）。
    x0 : 初始猜测，缺省为零向量。
    restart : 重启长度 m，决定工作内存 O(n * m)。
    tol : 相对残差容差，收敛判据为 ||r|| <= tol * ||b||。
    max_restarts : 最大重启周期数（预算之一）。
    max_matvec : matvec 调用次数预算；缺省时由 max_restarts 与 restart 推出。
    stagnation_rtol : 相邻重启边界真实残差的相对改善小于该值时判停滞；
        传 None 可关闭停滞检测。

    返回
    ----
    GmresResult。达到预算（max_restarts 或 max_matvec）或停滞时
    converged=False，绝不伪称收敛。
    """
    if not isinstance(n, int) or isinstance(n, bool) or n < 1:
        raise ValueError(f"显式尺寸 n 必须为正整数，得到 {n!r}")
    if restart < 1:
        raise ValueError(f"重启长度 restart 必须 >= 1，得到 {restart!r}")
    if tol <= 0.0:
        raise ValueError(f"容差 tol 必须为正，得到 {tol!r}")
    if max_restarts < 0:
        raise ValueError(f"max_restarts 必须 >= 0，得到 {max_restarts!r}")
    _check_input_vector(b, n, "b")
    b = [float(v) for v in b]
    if x0 is None:
        x = [0.0] * n
    else:
        _check_input_vector(x0, n, "x0")
        x = [float(v) for v in x0]
    if max_matvec is None:
        budget = (max_restarts + 1) * (restart + 1) + 1
    else:
        if max_matvec < 0:
            raise ValueError(f"max_matvec 必须 >= 0，得到 {max_matvec!r}")
        budget = max_matvec

    norm_b = _norm(b)
    if norm_b == 0.0:
        # 零右端：直接返回，不做任何 matvec。注意：若 x0 非零，不校验其残差。
        return GmresResult(
            x=x,
            converged=True,
            reason=REASON_ZERO_RHS,
            restart_cycles=0,
            arnoldi_steps=0,
            matvec_count=0,
            residual_norm=0.0,
            residual_history=[],
            restart_residuals=[0.0],
        )

    matvec_count = 0
    history: List[float] = []
    restart_residuals: List[float] = []

    def apply_a(v: Sequence[float], where: str) -> List[float]:
        nonlocal matvec_count
        try:
            y = matvec(list(v), n)
        except GmresError:
            raise
        except Exception as exc:
            raise MatvecError(
                f"{where}: matvec 回调抛出 {type(exc).__name__}: {exc}"
            ) from exc
        matvec_count += 1
        return _check_matvec_output(y, n, where)

    # 初始残差 r = b - A x0（x0 为零向量时省去一次 matvec）。
    if any(v != 0.0 for v in x):
        if matvec_count >= budget:
            return GmresResult(x, False, REASON_BUDGET, 0, 0, matvec_count,
                               float("nan"), history, restart_residuals)
        ax = apply_a(x, "初始残差计算 (r = b - A x0)")
        r = [bi - ai for bi, ai in zip(b, ax)]
    else:
        r = list(b)
    beta = _norm(r)
    restart_residuals.append(beta)
    if beta <= tol * norm_b:
        return GmresResult(x, True, REASON_CONVERGED, 0, 0, matvec_count,
                           beta, history, restart_residuals)

    cycles = 0
    steps = 0
    prev_beta = beta
    reason = REASON_BUDGET
    converged = False

    while cycles < max_restarts:
        if matvec_count >= budget:
            break
        cycles += 1
        m = restart
        # 工作内存：V 至多 m+1 个 n 维向量，H 为 (m+1) x m。
        V: List[List[float]] = [[ri / beta for ri in r]]
        H = [[0.0] * m for _ in range(m + 1)]
        cs = [0.0] * m
        sn = [0.0] * m
        g = [0.0] * (m + 1)
        g[0] = beta
        inner_done = 0
        budget_hit = False

        for j in range(m):
            if matvec_count >= budget:
                budget_hit = True
                break
            w = apply_a(V[j], f"重启周期 {cycles} / Arnoldi 步 {j}")
            steps += 1
            # 修正 Gram-Schmidt 正交化。
            for i in range(j + 1):
                hij = _dot(V[i], w)
                H[i][j] = hij
                if hij != 0.0:
                    vi = V[i]
                    w = [wk - hij * vik for wk, vik in zip(w, vi)]
            h_next = _norm(w)
            H[j + 1][j] = h_next
            # 套用之前的 Givens 旋转。
            for i in range(j):
                tmp = cs[i] * H[i][j] + sn[i] * H[i + 1][j]
                H[i + 1][j] = -sn[i] * H[i][j] + cs[i] * H[i + 1][j]
                H[i][j] = tmp
            # 生成并套用新的 Givens 旋转，残差估计即 |g[j+1]|。
            cs[j], sn[j] = _givens(H[j][j], H[j + 1][j])
            H[j][j] = cs[j] * H[j][j] + sn[j] * H[j + 1][j]
            H[j + 1][j] = 0.0
            tmp = cs[j] * g[j] + sn[j] * g[j + 1]
            g[j + 1] = -sn[j] * g[j] + cs[j] * g[j + 1]
            g[j] = tmp
            resid = abs(g[j + 1])
            history.append(resid)
            inner_done = j + 1
            if resid <= tol * norm_b:
                break
            scale = max(1.0, math.hypot(*(H[i][j] for i in range(j + 1))))
            if h_next <= _BREAKDOWN_RTOL * scale:
                break  # 快乐崩溃：Krylov 子空间已不变，继续 Arnoldi 无意义。
            V.append([wk / h_next for wk in w])

        if inner_done > 0:
            # 回代求解上三角系统 R y = g（取前 inner_done 列）。
            y = [0.0] * inner_done
            for i in range(inner_done - 1, -1, -1):
                rhs = g[i]
                for k in range(i + 1, inner_done):
                    rhs -= H[i][k] * y[k]
                diag = H[i][i]
                y[i] = 0.0 if diag == 0.0 else rhs / diag
            for i in range(inner_done):
                yi = y[i]
                if yi != 0.0:
                    vi = V[i]
                    for k in range(n):
                        x[k] += yi * vi[k]

        if budget_hit or matvec_count >= budget:
            # 预算耗尽：无法重算真实残差，记录 Arnoldi 估计，绝不伪称收敛。
            beta = abs(g[inner_done])
            restart_residuals.append(beta)
            reason = REASON_BUDGET
            break

        ax = apply_a(x, f"重启周期 {cycles} 末残差重算 (r = b - A x)")
        r = [bi - ai for bi, ai in zip(b, ax)]
        new_beta = _norm(r)
        if not math.isfinite(new_beta):
            raise GmresError(f"重启周期 {cycles} 末真实残差为非有限值")
        restart_residuals.append(new_beta)
        if new_beta <= tol * norm_b:
            beta = new_beta
            converged = True
            reason = REASON_CONVERGED
            break
        if (stagnation_rtol is not None
                and new_beta >= prev_beta * (1.0 - stagnation_rtol)):
            beta = new_beta
            reason = REASON_STAGNATION
            break
        prev_beta = new_beta
        beta = new_beta

    return GmresResult(
        x=x,
        converged=converged,
        reason=reason,
        restart_cycles=cycles,
        arnoldi_steps=steps,
        matvec_count=matvec_count,
        residual_norm=beta,
        residual_history=history,
        restart_residuals=restart_residuals,
    )
