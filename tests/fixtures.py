from spectrum_integrator import Band, BandRuleSet, SpectrumFrame

SAMPLE_RATE = 4.0
FRAME_LENGTH = 4
HOP_SAMPLES = 4


def frame_zero() -> SpectrumFrame:
    return SpectrumFrame(
        frame_id="frame-000",
        start_sample=0,
        samples=(1.0, 2.0, 2.0, 1.0),
        sample_rate=SAMPLE_RATE,
        frame_version="spectrum-v1",
        source="tests/fixtures.py:frame_zero",
    )


def frame_zero_corrected() -> SpectrumFrame:
    return SpectrumFrame(
        frame_id="frame-000",
        start_sample=0,
        samples=(1.0, 2.0, 2.0, 2.0),
        sample_rate=SAMPLE_RATE,
        frame_version="spectrum-v2",
        source="tests/fixtures.py:frame_zero_corrected",
    )


def frame_four_silence() -> SpectrumFrame:
    return SpectrumFrame(
        frame_id="frame-004",
        start_sample=4,
        samples=(0.0, 0.0, 0.0, 0.0),
        sample_rate=SAMPLE_RATE,
        frame_version="spectrum-v1",
        source="tests/fixtures.py:frame_four_silence",
    )


def frame_four_corrected() -> SpectrumFrame:
    return SpectrumFrame(
        frame_id="frame-004",
        start_sample=4,
        samples=(1.0, 1.0, 1.0, 0.0),
        sample_rate=SAMPLE_RATE,
        frame_version="spectrum-v2",
        source="tests/fixtures.py:frame_four_corrected",
    )


def frame_eight_tone() -> SpectrumFrame:
    return SpectrumFrame(
        frame_id="frame-008",
        start_sample=8,
        samples=(1.0, 0.0, -1.0, 0.0),
        sample_rate=SAMPLE_RATE,
        frame_version="spectrum-v1",
        source="tests/fixtures.py:frame_eight_tone",
    )


def narrow_rules() -> BandRuleSet:
    return BandRuleSet(
        band_version="bands-v1-narrow",
        bands=(
            Band("low", 0.0, 0.5),
            Band("high", 1.0, 2.0),
        ),
    )


def widened_rules() -> BandRuleSet:
    return BandRuleSet(
        band_version="bands-v2-widened",
        bands=(
            Band("low", 0.0, 1.0),
            Band("high", 1.0, 2.0),
        ),
    )


def full_cover_rules() -> BandRuleSet:
    return BandRuleSet(
        band_version="bands-full-partition-v1",
        bands=(
            Band("low_half", 0.0, 1.0),
            Band("high_half", 1.0, 2.0),
        ),
    )


def nested_overlap_rules() -> BandRuleSet:
    return BandRuleSet(
        band_version="bands-nested-v1",
        bands=(
            Band("full", 0.0, 2.0),
            Band("inner", 0.0, 1.0),
        ),
    )
