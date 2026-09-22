class IndependentWatermarkOracle:
    def __init__(self):
        self.delivered = {"A": set(), "B": set()}
        self.acked = {"A": set(), "B": set()}
        self.progress = {}
        self.idle = set()
        self.boundaries = []
        self.boundary = 0

    def prefix(self, values):
        prefix = 0
        while prefix + 1 in values:
            prefix += 1
        return prefix

    def apply(self, action):
        if action.startswith("d"):
            seq = int(action[1:])
            self.delivered["A"].add(seq)
        elif action.startswith("a"):
            seq = int(action[1:])
            self.acked["A"].add(seq)
        elif action == "pA":
            self.progress["A"] = (100, 3)
        elif action == "pB":
            self.progress["B"] = (80, 0)
        elif action == "iB":
            self.idle.add("B")
        self.check()

    def check(self):
        candidates = []
        for source in ("A", "B"):
            delivered_prefix = self.prefix(self.delivered[source])
            acked_prefix = self.prefix(self.acked[source])
            if source in self.progress:
                frontier, cut = self.progress[source]
            else:
                frontier, cut = 0, 0
            gap = cut > delivered_prefix
            unacked = any(seq <= delivered_prefix for seq in self.delivered[source] - self.acked[source])
            if gap or unacked:
                candidates.append(self.boundary)
            elif source in self.idle:
                continue
            else:
                offered = frontier if acked_prefix >= cut else self.boundary
                candidates.append(offered)
        if candidates:
            target = min(candidates)
            if target > self.boundary:
                self.boundary = target
                self.boundaries.append(target)
