# 两个符号哈希索引给出不同导出

准入层按一个符号索引审查插件，运行时按另一个索引查找，恶意插件可能向两边呈现不同导出。为Go审查宿主实现GNU/SysV风格双索引核验包，输入是已解码但不可信的表数组及索引0为空的符号名表，非空名字唯一且按UTF-8原字节哈希。GNU哈希从5381起按h=33h+字节模2^32；表含nbuckets、symoffset、2的幂个64位Bloom字、0至31的shift、buckets与chains，Bloom位置为(h/64)模字数，两个位为h模64和(h>>shift)模64，bucket从h模nbuckets取起始符号号，chain从号减symoffset取值，最低位为终止位，其余位与哈希忽略最低位比较，最终仍比较名字。SysV哈希从0起逐字节h=16h+字节，g=h&0xf0000000，h=(h XOR (g>>24)) AND NOT g；按bucket与next数组追踪直到0。GNU表仅覆盖symoffset至表尾，SysV表覆盖全部非空符号；GNU表仅覆盖symoffset至表尾，SysV表覆盖全部非空符号；逐个检查symoffset至表尾的候选导出，两索引均须与线性名字查找返回同一符号号；检查数组长度、索引界、链环及工作预算，拒绝任何索引藏掉或改绑导出。用分别损坏Bloom、终止位、bucket与next的夹具输出具体名字及两条查找路径，不把Bloom误报本身当攻击。

## API

```go
type GNUTable struct {
    NBuckets   uint32
    SymOffset  uint32
    Bloom      []uint64
    BloomShift uint32
    Buckets    []uint32
    Chains     []uint32
}

type SysVTable struct {
    Buckets []uint32
    Next    []uint32
}

func GNUHash(name string) uint32
func SysVHash(name string) uint32

func (t *GNUTable) Lookup(names []string, name string) LookupResult
func (t *SysVTable) Lookup(names []string, name string) LookupResult
func CheckName(names []string, name string, gnu *GNUTable, sysv *SysVTable) NameCheck

func Verify(names []string, gnu *GNUTable, sysv *SysVTable, opts ...Option) *Report
func VerifyDual(names []string, gnu *GNUTable, sysv *SysVTable, opts ...Option) (*Report, error)
func Check(names []string, gnu *GNUTable, sysv *SysVTable, opts ...Option) error
```

`LookupResult.String()` and `NameCheck.String()` render the full path:
hash, Bloom word and both bits, bucket, start symbol, every chain/next
word, and the final match. `Report.Checks` contains those paths for every
nonempty export; `Report.Issues` names each rejected export and carries the
same two paths when they were computed.

`Verify` enforces table lengths, power-of-two Bloom size, a 0..31 shift,
index bounds, GNU termination bits, System V cycles, chain overlap,
work budgets, and exact dual-index agreement with linear name lookup.
GNU is checked from `SymOffset` to the end; System V is checked for every
nonempty symbol. A Bloom false positive is deliberately not an issue: only
an export that the GNU lookup cannot find is rejected.

The corruption fixtures live in `fixture_test.go` and cover a cleared Bloom
bit, a cleared GNU termination bit, a zeroed bucket, System V `next` cycles,
and a truncated System V chain. Run them with:

```sh
go test -v ./...
```
