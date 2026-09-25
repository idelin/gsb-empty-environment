"""运行样例：python3.11 demo.py"""

from fractions import Fraction as F

from minrect import PointCloud, convex_hull, min_area_rectangle, verify_certificate


def show(title, points):
    print(f"== {title} ==")
    print("点集:      ", points)
    print("凸包:      ", convex_hull(points))
    cert = min_area_rectangle(points)
    print("面积:      ", cert.area)
    print("支撑边:    ", cert.support_edge)
    print("方向向量:  ", cert.direction)
    print("四角:      ", list(cert.corners))
    print("沿向投影:  ", cert.proj_along)
    print("法向投影:  ", cert.proj_across)
    print("极值点:    ", list(cert.extremes))
    print("逐边枚举核验:", "通过" if verify_certificate(points, cert) else "失败")
    print()


def main():
    show("轴对齐矩形", [(0, 0), (4, 0), (4, 3), (0, 3)])
    show("旋转矩形(3-4-5 方向, 5x2)",
         [(0, 0), (3, 4), (F(7, 5), F(26, 5)), (F(-8, 5), F(6, 5))])
    show("近共线大坐标", [(0, 0), (10**9, 1), (2 * 10**9, 0)])
    show("等面积多解三角形", [(0, 0), (4, 0), (1, 2)])
    show("单点", [(7, -3)])
    show("共线", [(0, 0), (1, 1), (2, 2)])

    print("== 批量撤回 ==")
    cloud = PointCloud()
    handles = cloud.add_batch([(0, 0), (4, 0), (4, 3), (0, 3), (1, 1)])
    print("句柄分配:  ", handles)
    cert = cloud.min_area_rectangle()
    print("撤回前面积:", cert.area, " 极值句柄:", cert.extreme_handles)
    cloud.remove_batch([1, 3])
    print("撤回句柄 1,3 即 (4,0),(0,3)")
    cert = cloud.min_area_rectangle()
    print("撤回后面积:", cert.area, " 极值句柄:", cert.extreme_handles)
    print("逐边枚举核验:", "通过" if verify_certificate(cloud.points(), cert) else "失败")


if __name__ == "__main__":
    main()
