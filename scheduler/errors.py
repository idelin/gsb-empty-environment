class InputError(Exception):
    """本地 JSON 构建输入不合法。"""


class InfeasibleError(Exception):
    """资源容量下问题不可行（如单任务需求超过容量）。"""
