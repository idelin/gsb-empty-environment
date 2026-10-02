/**
 * 增量动态 AABB 树（Box2D b2DynamicTree 风格）。
 *
 * - 叶节点保存实体的（膨胀后）包围盒与用户数据，内部节点保存子树的联合包围盒。
 * - 插入采用 SAH（表面积启发式）选择最优兄弟节点，删除/插入后通过旋转保持平衡。
 * - 查询、自相交遍历均为 O(log N + K)，K 为命中数量。
 *
 * 节点存放在连续数组中，以索引代替指针，空闲节点串成 freelist 复用。
 */
import { cloneAabb, overlaps, surfaceArea, union, unionInto, unionSurfaceArea } from "./aabb.ts";
import type { AABB } from "./aabb.ts";

const NULL = -1;

interface TreeNode<T> {
  aabb: AABB;
  /** 父节点索引；节点空闲时复用为 freelist 的 next 指针。 */
  parent: number;
  child1: number;
  child2: number;
  /** 叶节点为 0，空闲节点为 -1。 */
  height: number;
  userData: T | undefined;
}

export class DynamicAabbTree<T> {
  root: number = NULL;
  nodes: Array<TreeNode<T>> = [];
  nodeCount = 0;
  freeList: number = NULL;
  private leafCount = 0;
  /** query 复用的遍历栈，避免每次查询分配数组。 */
  private readonly queryStack: number[] = [];
  /** forEachOverlappingPair 复用的节点对栈（扁平存储，避免分配）。 */
  private readonly pairStack: number[] = [];

  get proxyCount(): number {
    return this.leafCount;
  }

  getHeight(): number {
    return this.root === NULL ? 0 : this.nodes[this.root].height;
  }

  /** 创建叶代理并插入树，返回代理 id。 */
  createProxy(aabb: AABB, userData: T): number {
    const proxyId = this.allocateNode();
    const node = this.nodes[proxyId];
    node.aabb = cloneAabb(aabb);
    node.userData = userData;
    node.height = 0;
    node.child1 = NULL;
    node.child2 = NULL;
    this.insertLeaf(proxyId);
    this.leafCount++;
    return proxyId;
  }

  /** 从树中移除代理。 */
  destroyProxy(proxyId: number): void {
    const node = this.nodes[proxyId];
    if (node.height !== 0) {
      throw new Error(`destroyProxy: node ${proxyId} is not a leaf`);
    }
    this.removeLeaf(proxyId);
    this.freeNode(proxyId);
    this.leafCount--;
  }

  /** 以新的包围盒重新定位代理（先摘除再插入，保持树结构合法）。 */
  moveProxy(proxyId: number, aabb: AABB): void {
    const node = this.nodes[proxyId];
    if (node.height !== 0) {
      throw new Error(`moveProxy: node ${proxyId} is not a leaf`);
    }
    this.removeLeaf(proxyId);
    const target = node.aabb;
    target.minX = aabb.minX;
    target.minY = aabb.minY;
    target.minZ = aabb.minZ;
    target.maxX = aabb.maxX;
    target.maxY = aabb.maxY;
    target.maxZ = aabb.maxZ;
    this.insertLeaf(proxyId);
  }

  getFatAabb(proxyId: number): AABB {
    return this.nodes[proxyId].aabb;
  }

  getUserData(proxyId: number): T {
    return this.nodes[proxyId].userData as T;
  }

  /** 范围查询：对所有与 aabb 相交的叶节点触发回调。 */
  query(aabb: AABB, callback: (userData: T, proxyId: number) => void): void {
    if (this.root === NULL) return;
    const stack = this.queryStack;
    stack.length = 0;
    stack.push(this.root);
    while (stack.length > 0) {
      const nodeId = stack.pop() as number;
      const node = this.nodes[nodeId];
      if (!overlaps(node.aabb, aabb)) continue;
      if (node.child1 === NULL) {
        callback(node.userData as T, nodeId);
      } else {
        stack.push(node.child1, node.child2);
      }
    }
  }

  /**
   * 树内自相交遍历：枚举所有包围盒互相重叠的叶节点对。
   * 以节点对栈自顶向下展开，复杂度 O(N log N + K)。
    * 工作项 (a, b)：a === b 表示对节点 a 做子树自碰撞，否则为交叉对。
   */
  forEachOverlappingPair(callback: (a: T, b: T) => void): void {
    if (this.root === NULL) return;
    const root = this.nodes[this.root];
    if (root.child1 === NULL) return;
    const stack = this.pairStack;
    stack.length = 0;
    stack.push(root.child1, root.child1, root.child2, root.child2, root.child1, root.child2);
    while (stack.length > 0) {
      const b = stack.pop() as number;
      const a = stack.pop() as number;
      if (a === b) {
        // 子树自碰撞：展开为 子子树自碰撞 x2 + 交叉对 x1
        const node = this.nodes[a];
        if (node.child1 === NULL) continue;
        stack.push(node.child1, node.child1, node.child2, node.child2, node.child1, node.child2);
        continue;
      }
      const nodeA = this.nodes[a];
      const nodeB = this.nodes[b];
      if (!overlaps(nodeA.aabb, nodeB.aabb)) continue;
      const aIsLeaf = nodeA.child1 === NULL;
      const bIsLeaf = nodeB.child1 === NULL;
      if (aIsLeaf && bIsLeaf) {
        callback(nodeA.userData as T, nodeB.userData as T);
      } else if (bIsLeaf || (!aIsLeaf && nodeA.height >= nodeB.height)) {
        stack.push(nodeA.child1, b, nodeA.child2, b);
      } else {
        stack.push(a, nodeB.child1, a, nodeB.child2);
      }
    }
  }

  private allocateNode(): number {
    if (this.freeList !== NULL) {
      const nodeId = this.freeList;
      this.freeList = this.nodes[nodeId].parent;
      const node = this.nodes[nodeId];
      node.parent = NULL;
      node.child1 = NULL;
      node.child2 = NULL;
      node.height = 0;
      node.userData = undefined;
      this.nodeCount++;
      return nodeId;
    }
    const nodeId = this.nodes.length;
    this.nodes.push({
      aabb: { minX: 0, minY: 0, minZ: 0, maxX: 0, maxY: 0, maxZ: 0 },
      parent: NULL,
      child1: NULL,
      child2: NULL,
      height: 0,
      userData: undefined,
    });
    this.nodeCount++;
    return nodeId;
  }

  private freeNode(nodeId: number): void {
    const node = this.nodes[nodeId];
    node.parent = this.freeList;
    node.height = -1;
    node.userData = undefined;
    this.freeList = nodeId;
    this.nodeCount--;
  }

  private insertLeaf(leaf: number): void {
    if (this.root === NULL) {
      this.root = leaf;
      this.nodes[leaf].parent = NULL;
      return;
    }

    const leafAabb = this.nodes[leaf].aabb;

    // 1) SAH 启发式寻找代价最小的兄弟节点。
    let index = this.root;
    while (this.nodes[index].child1 !== NULL) {
      const node = this.nodes[index];
      const area = surfaceArea(node.aabb);
      const combinedArea = unionSurfaceArea(node.aabb, leafAabb);

      const cost = 2 * combinedArea;
      const inheritanceCost = 2 * (combinedArea - area);

      const child1 = node.child1;
      const child2 = node.child2;
      const cost1 = this.descendCost(child1, leafAabb, inheritanceCost);
      const cost2 = this.descendCost(child2, leafAabb, inheritanceCost);

      if (cost < cost1 && cost < cost2) break;
      index = cost1 < cost2 ? child1 : child2;
    }

    // 2) 为叶节点与兄弟节点新建共同父节点。
    const sibling = index;
    const oldParent = this.nodes[sibling].parent;
    const newParent = this.allocateNode();
    const parentNode = this.nodes[newParent];
    parentNode.parent = oldParent;
    parentNode.userData = undefined;
    parentNode.aabb = union(leafAabb, this.nodes[sibling].aabb);
    parentNode.height = this.nodes[sibling].height + 1;

    if (oldParent !== NULL) {
      if (this.nodes[oldParent].child1 === sibling) {
        this.nodes[oldParent].child1 = newParent;
      } else {
        this.nodes[oldParent].child2 = newParent;
      }
    } else {
      this.root = newParent;
    }
    parentNode.child1 = sibling;
    parentNode.child2 = leaf;
    this.nodes[sibling].parent = newParent;
    this.nodes[leaf].parent = newParent;

    // 3) 自底向上修复包围盒与高度，并旋转保持平衡。
    let walk = this.nodes[leaf].parent;
    while (walk !== NULL) {
      walk = this.balance(walk);
      const child1 = this.nodes[walk].child1;
      const child2 = this.nodes[walk].child2;
      this.nodes[walk].height =
        1 + Math.max(this.nodes[child1].height, this.nodes[child2].height);
      unionInto(this.nodes[walk].aabb, this.nodes[child1].aabb, this.nodes[child2].aabb);
      walk = this.nodes[walk].parent;
    }
  }

  private descendCost(childId: number, leafAabb: AABB, inheritanceCost: number): number {
    const child = this.nodes[childId];
    const newArea = unionSurfaceArea(leafAabb, child.aabb);
    if (child.child1 === NULL) {
      return newArea + inheritanceCost;
    }
    const oldArea = surfaceArea(child.aabb);
    return newArea - oldArea + inheritanceCost;
  }

  private removeLeaf(leaf: number): void {
    if (leaf === this.root) {
      this.root = NULL;
      return;
    }

    const parent = this.nodes[leaf].parent;
    const grandParent = this.nodes[parent].parent;
    const sibling =
      this.nodes[parent].child1 === leaf
        ? this.nodes[parent].child2
        : this.nodes[parent].child1;

    if (grandParent !== NULL) {
      if (this.nodes[grandParent].child1 === parent) {
        this.nodes[grandParent].child1 = sibling;
      } else {
        this.nodes[grandParent].child2 = sibling;
      }
      this.nodes[sibling].parent = grandParent;
      this.freeNode(parent);

      let walk = grandParent;
      while (walk !== NULL) {
        walk = this.balance(walk);
        const child1 = this.nodes[walk].child1;
        const child2 = this.nodes[walk].child2;
        unionInto(this.nodes[walk].aabb, this.nodes[child1].aabb, this.nodes[child2].aabb);
        this.nodes[walk].height =
          1 + Math.max(this.nodes[child1].height, this.nodes[child2].height);
        walk = this.nodes[walk].parent;
      }
    } else {
      this.root = sibling;
      this.nodes[sibling].parent = NULL;
      this.freeNode(parent);
    }
  }

  /** AVL 式旋转：高度差超过 1 时将较高的子节点上旋。 */
  private balance(iA: number): number {
    const A = this.nodes[iA];
    if (A.child1 === NULL || A.height < 2) return iA;

    const iB = A.child1;
    const iC = A.child2;
    const B = this.nodes[iB];
    const C = this.nodes[iC];
    const balance = C.height - B.height;

    if (balance > 1) {
      // C 上旋。
      const iF = C.child1;
      const iG = C.child2;
      const F = this.nodes[iF];
      const G = this.nodes[iG];

      C.child1 = iA;
      C.parent = A.parent;
      A.parent = iC;
      if (C.parent !== NULL) {
        if (this.nodes[C.parent].child1 === iA) {
          this.nodes[C.parent].child1 = iC;
        } else {
          this.nodes[C.parent].child2 = iC;
        }
      } else {
        this.root = iC;
      }

      if (F.height > G.height) {
        C.child2 = iF;
        A.child2 = iG;
        G.parent = iA;
        unionInto(A.aabb, B.aabb, G.aabb);
        unionInto(C.aabb, A.aabb, F.aabb);
        A.height = 1 + Math.max(B.height, G.height);
        C.height = 1 + Math.max(A.height, F.height);
      } else {
        C.child2 = iG;
        A.child2 = iF;
        F.parent = iA;
        unionInto(A.aabb, B.aabb, F.aabb);
        unionInto(C.aabb, A.aabb, G.aabb);
        A.height = 1 + Math.max(B.height, F.height);
        C.height = 1 + Math.max(A.height, G.height);
      }
      return iC;
    }

    if (balance < -1) {
      // B 上旋（镜像）。
      const iF = B.child1;
      const iG = B.child2;
      const F = this.nodes[iF];
      const G = this.nodes[iG];

      B.child1 = iA;
      B.parent = A.parent;
      A.parent = iB;
      if (B.parent !== NULL) {
        if (this.nodes[B.parent].child1 === iA) {
          this.nodes[B.parent].child1 = iB;
        } else {
          this.nodes[B.parent].child2 = iB;
        }
      } else {
        this.root = iB;
      }

      if (F.height > G.height) {
        B.child2 = iF;
        A.child1 = iG;
        G.parent = iA;
        unionInto(A.aabb, C.aabb, G.aabb);
        unionInto(B.aabb, A.aabb, F.aabb);
        A.height = 1 + Math.max(C.height, G.height);
        B.height = 1 + Math.max(A.height, F.height);
      } else {
        B.child2 = iG;
        A.child1 = iF;
        F.parent = iA;
        unionInto(A.aabb, C.aabb, F.aabb);
        unionInto(B.aabb, A.aabb, G.aabb);
        A.height = 1 + Math.max(C.height, F.height);
        B.height = 1 + Math.max(A.height, G.height);
      }
      return iB;
    }

    return iA;
  }

  /** 校验树的结构不变量（供测试使用）。 */
  validate(): void {
    if (this.root === NULL) {
      if (this.nodeCount !== 0) throw new Error("empty root but nodeCount != 0");
      return;
    }
    if (this.nodes[this.root].parent !== NULL) {
      throw new Error("root has a parent");
    }

    let freeCount = 0;
    let id = this.freeList;
    while (id !== NULL) {
      if (this.nodes[id].height !== -1) throw new Error("free node height != -1");
      freeCount++;
      id = this.nodes[id].parent;
    }
    if (freeCount + this.nodeCount !== this.nodes.length) {
      throw new Error("node accounting mismatch");
    }

    let leafCount = 0;
    const visit = (nodeId: number): void => {
      const node = this.nodes[nodeId];
      if (node.child1 === NULL) {
        if (node.height !== 0) throw new Error("leaf height != 0");
        leafCount++;
        return;
      }
      const child1 = this.nodes[node.child1];
      const child2 = this.nodes[node.child2];
      if (child1.parent !== nodeId || child2.parent !== nodeId) {
        throw new Error("broken parent link");
      }
      const expectedHeight = 1 + Math.max(child1.height, child2.height);
      if (node.height !== expectedHeight) throw new Error("height mismatch");
      const expected = union(child1.aabb, child2.aabb);
      const eps = 1e-9;
      if (
        Math.abs(node.aabb.minX - expected.minX) > eps ||
        Math.abs(node.aabb.minY - expected.minY) > eps ||
        Math.abs(node.aabb.minZ - expected.minZ) > eps ||
        Math.abs(node.aabb.maxX - expected.maxX) > eps ||
        Math.abs(node.aabb.maxY - expected.maxY) > eps ||
        Math.abs(node.aabb.maxZ - expected.maxZ) > eps
      ) {
        throw new Error("aabb mismatch");
      }
      visit(node.child1);
      visit(node.child2);
    };
    visit(this.root);
    // 满二叉树：总节点数 = 2 * 叶节点数 - 1
    if (2 * leafCount - 1 !== this.nodeCount) throw new Error("leaf count mismatch");
  }
}
