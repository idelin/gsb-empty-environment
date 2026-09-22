"""Explicit finite-state transducers and bounded streaming composition."""

from .core import NotAccepted, RejectedInput, Transducer, TransducerError
from .pipeline import (
    BoundedBuffer,
    BufferFull,
    CollectSink,
    Pipeline,
    PipelineError,
    PipelineStats,
    run_pipeline,
)
from .reference import compose, run_transducer

__all__ = [
    "BoundedBuffer",
    "BufferFull",
    "CollectSink",
    "NotAccepted",
    "Pipeline",
    "PipelineError",
    "PipelineStats",
    "RejectedInput",
    "Transducer",
    "TransducerError",
    "compose",
    "run_pipeline",
    "run_transducer",
]
