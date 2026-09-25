"""各处理级的遥测:声明延迟、增益轨迹、阈值轨迹、超限证据。"""
from __future__ import annotations

from dataclasses import dataclass, field


@dataclass
class StageTelemetry:
    name: str
    latency_samples: int
    gain_db: list = field(default_factory=list)       # 每样本实际施加的增益(dB)
    threshold_db: list = field(default_factory=list)  # 每样本平滑后的阈值(dB)
    events: list = field(default_factory=list)        # 超限证据事件
