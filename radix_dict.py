"""前缀条件压缩字典：带权压缩基数树（radix tree），支持插入、删除与前缀 TopK。

排序规则：权重降序，权重相同按 Unicode 码点字典序升序。
权重更新沿根到目标词路径增量维护每节点堆，不做全量排序。
删除后对基数树做单子节点合并压缩。

命令行接口（stdin 逐行读命令，stdout 输出结果）：
    INSERT <word> <weight>   插入或更新词及其权重（upsert），输出 OK
    DELETE <word>            删除词，输出 OK 或 NOT_FOUND
    GET <word>               精确查询，输出 "<word> <weight>" 或 NOT_FOUND
    TOPK <k> <prefix>        前缀 TopK，逐行输出 "<word> <weight>"；
                             空前缀用 "" 表示（即全词典）
    SIZE                     输出词典中词的数量
    CHECK                    结构不变量检查，输出 OK 或逐条错误
    QUIT                     退出
空行与 # 开头的行被忽略。word 不得包含空白字符。
"""

from __future__ import annotations

import heapq
import sys


def _common_prefix_len(a: str, b: str) -> int:
    n = min(len(a), len(b))
    i = 0
    while i < n and a[i] == b[i]:
        i += 1
    return i


class _Node:
    __slots__ = ("label", "children", "terminal", "weight", "heap")

    def __init__(self, label: str = "") -> None:
        self.label = label          # 父边标签（根为 ""）
        self.children = {}          # 子边首字符 -> _Node
        self.terminal = False       # 是否有词在此结束
        self.weight = 0.0           # terminal 词的权重
        # 子树全部词的懒删除最大堆，元素 (-weight, word, version)
        self.heap = []


class PrefixTopKDict:
    """带权字符串字典：压缩基数树 + 每节点懒删除堆。"""

    def __init__(self) -> None:
        self._root = _Node("")
        self._words = {}  # word -> (weight, version)
        self._next_version = 0  # 全局单调递增，保证删除重插后旧堆条目不会“复活”

    def __len__(self) -> int:
        return len(self._words)

    def __contains__(self, word: str) -> bool:
        return word in self._words

    def get(self, word: str):
        """精确查询，返回权重或 None。"""
        entry = self._words.get(word)
        return None if entry is None else entry[0]

    def insert(self, word: str, weight) -> None:
        """插入或更新（upsert）。仅沿路径增量维护堆，不做全量排序。"""
        if not word:
            raise ValueError("word must be non-empty")
        node = self._root
        path = [node]
        i = 0
        n = len(word)
        terminal_node = None
        while i < n:
            key = word[i]
            child = node.children.get(key)
            if child is None:
                child = _Node(word[i:])
                node.children[key] = child
                path.append(child)
                terminal_node = child
                break
            label = child.label
            j = _common_prefix_len(label, word[i:])
            if j == len(label):
                path.append(child)
                node = child
                i += j
                continue
            # 在 j 处分裂 child 边
            mid = _Node(label[:j])
            node.children[key] = mid
            child.label = label[j:]
            mid.children[child.label[0]] = child
            mid.heap = child.heap[:]  # 子树相同；拷贝以避免两节点共享堆列表
            path.append(mid)
            if i + j == n:
                terminal_node = mid
            else:
                leaf = _Node(word[i + j:])
                mid.children[word[i + j]] = leaf
                path.append(leaf)
                terminal_node = leaf
            break
        else:
            terminal_node = node

        version = self._next_version
        self._next_version += 1
        self._words[word] = (weight, version)
        terminal_node.terminal = True
        terminal_node.weight = weight
        for nd in path:
            heapq.heappush(nd.heap, (-weight, word, version))

    def delete(self, word: str) -> bool:
        """删除词；堆条目懒删除，树结构做单子节点合并压缩。"""
        if word not in self._words:
            return False
        del self._words[word]

        node = self._root
        stack = []
        i = 0
        while i < len(word):
            child = node.children[word[i]]
            stack.append(node)
            node = child
            i += len(child.label)
        node.terminal = False

        # 向上压缩：非终结且子节点数 <= 1 的节点需要消除
        while stack and not node.terminal and len(node.children) <= 1:
            parent = stack[-1]
            if not node.children:
                del parent.children[node.label[0]]
                node = parent
                stack.pop()
            else:
                child = next(iter(node.children.values()))
                node.label += child.label
                node.children = child.children
                node.terminal = child.terminal
                node.weight = child.weight
                node.heap = child.heap  # 活条目集合相同，直接接管
        return True

    def topk(self, prefix: str, k: int):
        """返回 [(word, weight), ...]，权重降序、同权重按码点字典序。"""
        if k <= 0:
            return []
        node = self._root
        i = 0
        while i < len(prefix):
            child = node.children.get(prefix[i])
            if child is None:
                return []
            label = child.label
            j = _common_prefix_len(label, prefix[i:])
            if j < len(label):
                if i + j < len(prefix):
                    return []  # 前缀与边标签不匹配
                node = child  # 前缀结束于边中间，整棵子树共享该前缀
                break
            node = child
            i += j

        results = []
        popped = []
        heap = node.heap
        words = self._words
        while heap and len(results) < k:
            entry = heapq.heappop(heap)
            popped.append(entry)
            neg_w, word, version = entry
            cur = words.get(word)
            if cur is not None and cur[1] == version and cur[0] == -neg_w:
                results.append((word, -neg_w))
        for entry in popped:
            heapq.heappush(heap, entry)
        return results

    def check_invariants(self):
        """结构不变量检查，返回错误描述列表（空列表表示通过）。

        检查项：
        1. 压缩性：非根、非终结节点不得恰好只有一个子节点；
        2. 边标签非空，children 键等于子边标签首字符；
        3. 每节点堆的活条目集合 == 该节点子树中的词集合，且均带该节点路径前缀；
        4. 树中终结节点与 _words 完全一致（词与权重）。
        """
        errors = []
        words = self._words

        def live_words(node):
            live = set()
            for neg_w, word, version in node.heap:
                cur = words.get(word)
                if cur is not None and cur[1] == version and cur[0] == -neg_w:
                    live.add(word)
            return live

        def walk(node, path, is_root):
            if not is_root:
                if not node.label:
                    errors.append(f"empty edge label at path {path!r}")
                if not node.terminal and len(node.children) == 1:
                    errors.append(f"uncompressed single-child node at {path!r}")
            subtree = set()
            if node.terminal:
                subtree.add(path)
                cur = words.get(path)
                if cur is None:
                    errors.append(f"terminal {path!r} missing from _words")
                elif cur[0] != node.weight:
                    errors.append(f"weight mismatch at {path!r}")
            for key, child in node.children.items():
                if not child.label or key != child.label[0]:
                    errors.append(f"child key {key!r} != label head at {path!r}")
                subtree |= walk(child, path + child.label, False)
            live = live_words(node)
            for word in live:
                if not word.startswith(path):
                    errors.append(f"heap entry {word!r} lacks prefix {path!r}")
            if live != subtree:
                errors.append(
                    f"heap/subtree mismatch at {path!r}: "
                    f"heap-only={sorted(live - subtree)!r} "
                    f"tree-only={sorted(subtree - live)!r}"
                )
            return subtree

        all_words = walk(self._root, "", True)
        if set(words) != all_words:
            errors.append(
                f"_words/tree mismatch: dict-only={sorted(set(words) - all_words)!r} "
                f"tree-only={sorted(all_words - set(words))!r}"
            )
        return errors


def _parse_weight(token: str):
    value = float(token)
    return int(value) if value.is_integer() else value


def _format_weight(weight) -> str:
    if isinstance(weight, float) and weight.is_integer():
        return str(int(weight))
    return str(weight)


def _parse_prefix(token: str) -> str:
    if token == '""':
        return ""
    return token


def main(stdin=None, stdout=None) -> int:
    stdin = sys.stdin if stdin is None else stdin
    stdout = sys.stdout if stdout is None else stdout
    dictionary = PrefixTopKDict()
    for line in stdin:
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        parts = line.split()
        cmd = parts[0].upper()
        if cmd == "INSERT":
            dictionary.insert(parts[1], _parse_weight(parts[2]))
            print("OK", file=stdout)
        elif cmd == "DELETE":
            print("OK" if dictionary.delete(parts[1]) else "NOT_FOUND", file=stdout)
        elif cmd == "GET":
            weight = dictionary.get(parts[1])
            if weight is None:
                print("NOT_FOUND", file=stdout)
            else:
                print(f"{parts[1]} {_format_weight(weight)}", file=stdout)
        elif cmd == "TOPK":
            k = int(parts[1])
            prefix = _parse_prefix(parts[2])
            for word, weight in dictionary.topk(prefix, k):
                print(f"{word} {_format_weight(weight)}", file=stdout)
        elif cmd == "SIZE":
            print(len(dictionary), file=stdout)
        elif cmd == "CHECK":
            errors = dictionary.check_invariants()
            if errors:
                for err in errors:
                    print(f"ERROR {err}", file=stdout)
            else:
                print("OK", file=stdout)
        elif cmd == "QUIT":
            break
        else:
            print(f"ERROR unknown command: {cmd}", file=stdout)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
