"""dpproj: seeded-noise release + exact rational QP projection with KKT
evidence. Synthetic data only; see README for definitions and the privacy
disclaimer."""
from .noise import NoiseLedger
from .pipeline import (ConsistencyConstraint, NoisedTarget, PublicTarget,
                       ReleaseConfig, ReleaseResult, run_release,
                       verify_release)
from .qp import (InfeasibleError, KKTCertificate, QPResult, solve_qp,
                 verify_kkt)
from .reference import solve_qp_enumeration
