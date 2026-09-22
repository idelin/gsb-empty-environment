class WatermarkError(Exception):
    pass


class ProtocolViolationError(WatermarkError):
    pass


class InvalidStateError(WatermarkError):
    pass


class UnknownSourceError(WatermarkError):
    pass


class StaleEpochError(WatermarkError):
    pass


class CorruptedLogError(WatermarkError):
    pass


class CheckpointChecksumError(WatermarkError):
    pass


class SimulatedProcessCrash(BaseException):
    def __init__(self, phase: str):
        super().__init__(f"simulated process crash: {phase}")
        self.phase = phase
