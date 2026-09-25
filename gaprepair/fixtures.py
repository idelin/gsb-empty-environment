"""Local fixtures: sample arrays and sample rate from JSON files.

No audio device is ever touched; missing samples are JSON nulls and are
converted to NaN on load.
"""

import json
import math
from pathlib import Path

FIXTURE_DIR = Path(__file__).resolve().parent.parent / "fixtures"


def load_fixture(path):
    data = json.loads(Path(path).read_text(encoding="utf-8"))
    samples = [math.nan if v is None else float(v) for v in data["samples"]]
    return samples, float(data["sample_rate"])
