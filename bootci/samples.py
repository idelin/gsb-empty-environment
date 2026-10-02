"""样本结构：把“重采样单位”显式编码进数据类型。

- OneSample：单位为单个观测。
- TwoSample：成组（独立）两样本，单位为各组内观测，两组分别重采样。
- PairedSample：配对样本，单位为“对”，必须成对抽取，禁止拆对。
- StratifiedSample：分层样本，单位为层内观测，层大小固定。
- TimeSeriesSample：时间序列，单位为“块”，禁止逐点独立抽样破坏相关性。
"""
from __future__ import annotations

from dataclasses import dataclass


def _check_values(values, name):
    vals = tuple(float(v) for v in values)
    if len(vals) == 0:
        raise ValueError(f"{name} 不能为空")
    return vals


@dataclass(frozen=True)
class OneSample:
    values: tuple
    sample_id: str = "sample"

    def __post_init__(self):
        object.__setattr__(self, "values", _check_values(self.values, "values"))

    @property
    def sizes(self):
        return {"n": len(self.values)}


@dataclass(frozen=True)
class TwoSample:
    """成组（独立）两样本：组内观测为重采样单位，组间独立。"""

    x: tuple
    y: tuple
    sample_id: str = "two-sample"

    def __post_init__(self):
        object.__setattr__(self, "x", _check_values(self.x, "x"))
        object.__setattr__(self, "y", _check_values(self.y, "y"))

    @property
    def sizes(self):
        return {"n_x": len(self.x), "n_y": len(self.y)}


@dataclass(frozen=True)
class PairedSample:
    """配对样本：重采样单位为“对”，x[i] 与 y[i] 必须同时被抽中。"""

    x: tuple
    y: tuple
    sample_id: str = "paired"

    def __post_init__(self):
        x = _check_values(self.x, "x")
        y = _check_values(self.y, "y")
        if len(x) != len(y):
            raise ValueError(f"配对样本长度必须一致，得到 {len(x)} 与 {len(y)}")
        object.__setattr__(self, "x", x)
        object.__setattr__(self, "y", y)

    @property
    def sizes(self):
        return {"n_pairs": len(self.x)}


@dataclass(frozen=True)
class StratifiedSample:
    """分层样本：每层内部独立重采样，层大小固定，层间不串扰。"""

    strata: tuple
    sample_id: str = "stratified"

    def __post_init__(self):
        strata = tuple(
            _check_values(s, f"strata[{i}]") for i, s in enumerate(self.strata)
        )
        if len(strata) == 0:
            raise ValueError("至少需要一个层")
        object.__setattr__(self, "strata", strata)

    @property
    def sizes(self):
        return {f"stratum_{i}": len(s) for i, s in enumerate(self.strata)}


@dataclass(frozen=True)
class TimeSeriesSample:
    """时间序列：重采样单位为长度 block_length 的重叠块（见 resample_mbb）。"""

    values: tuple
    sample_id: str = "series"

    def __post_init__(self):
        object.__setattr__(self, "values", _check_values(self.values, "values"))

    @property
    def sizes(self):
        return {"n": len(self.values)}
