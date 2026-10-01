"""demo.py — strided_tensor 运行样例。"""

from strided_tensor import BorrowError, OverlapError, ReshapeError, Tensor, Tri


def show(title, fn):
    print(f"== {title} ==")
    fn()
    print()


def main():
    def views():
        t = Tensor.from_nested([[1, 2, 3], [4, 5, 6]])
        print("原张量       :", t)
        print("转置视图     :", t.T)
        print("行翻转(负步长):", t.flip(0), "strides =", t.flip(0).strides)
        print("非连续切片   :", t[::1, ::2], "strides =", t[::1, ::2].strides)
        print("反向切片     :", Tensor.arange(6)[::-1])
    show("视图：转置 / 负步长 / 非连续切片", views)

    def broadcast():
        t = Tensor.from_flat([10, 20, 30], (3,))
        b = t.broadcast_to((4, 3))
        print("零步长广播   :", b, "strides =", b.strides)
        print("内部重叠判定 :", b.internal_overlap().name)
        try:
            b.fill(0)
        except OverlapError as e:
            print("写入广播视图被拒绝:", e)
    show("零步长广播与写保护", broadcast)

    def overlap_copy():
        t = Tensor.from_flat([1, 2, 3, 4], (4,))
        t[1:].copy_from(t[:-1])
        print("t[1:] = t[:-1] 快照语义:", t.flat_values(), "(非 [1,1,1,1])")
        t2 = Tensor.from_flat([1, 2, 3, 4], (4,))
        t2[:] = t2[::-1]
        print("t[:] = t[::-1] 反转   :", t2.flat_values())
        t3 = Tensor.from_flat([1, 2, 3], (3,))
        t3 += t3[::-1]
        print("t += t[::-1]          :", t3.flat_values())
    show("交叠拷贝的快照语义", overlap_copy)

    def borrow():
        t = Tensor.arange(4)
        with t.borrow():
            try:
                t[0] = 99
            except BorrowError as e:
                print("借用期内写入被拒绝:", e)
        t[0] = 99
        print("借用结束后可写    :", t.flat_values())
    show("借用期写保护", borrow)

    def reshape():
        t = Tensor.from_nested([[1, 2, 3], [4, 5, 6]])
        try:
            t.T.reshape((6,))
        except ReshapeError as e:
            print("转置后 reshape 被拒绝:", e)
        print("copy=True 复制 reshape:", t.T.reshape((6,), copy=True).flat_values())
        r = Tensor.arange(6).reshape((2, 3))
        print("连续视图 reshape 共享存储:", r)
    show("reshape：非连续拒绝或复制", reshape)

    def bounds():
        try:
            Tensor([0] * 8, (3,), (4,), 0)
        except IndexError as e:
            print("越界(按地址区间)被拒绝:", e)
    show("越界检查不只看元素总数", bounds)

    def empty():
        t = Tensor.from_flat([], (0,))
        print("空维张量:", t, "numel =", t.numel,
              "内部重叠 =", t.internal_overlap().name)
    show("空维处理", empty)


if __name__ == "__main__":
    main()
