"""追加式事件日志:每条事件带单调递增位置,落盘即持久。"""
import json
import os


class Journal:
    def __init__(self, path):
        self.path = path
        self.next_pos = 1
        if os.path.exists(path):
            with open(path, encoding="utf-8") as f:
                for line in f:
                    line = line.strip()
                    if line:
                        self.next_pos = max(self.next_pos, json.loads(line)["pos"] + 1)

    def append(self, event):
        event = dict(event)
        event["pos"] = self.next_pos
        with open(self.path, "a", encoding="utf-8") as f:
            f.write(json.dumps(event, ensure_ascii=False, sort_keys=True) + "\n")
            f.flush()
            os.fsync(f.fileno())
        self.next_pos += 1
        return event["pos"]

    def read_all(self):
        return list(self.read_from(1))

    def read_from(self, pos):
        events = []
        if not os.path.exists(self.path):
            return events
        with open(self.path, encoding="utf-8") as f:
            for line in f:
                line = line.strip()
                if not line:
                    continue
                event = json.loads(line)
                if event["pos"] >= pos:
                    events.append(event)
        return events
