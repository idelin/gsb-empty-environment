package mvcc

// 堆元组 t_infomask 标志位，取值与 PostgreSQL 头定义保持一致。
const (
	HeapHasNull          uint16 = 0x0001
	HeapHasVarWidth      uint16 = 0x0002
	HeapHasExternal      uint16 = 0x0004
	HeapXmaxKeyShareLock uint16 = 0x0010
	HeapComboCid         uint16 = 0x0020
	HeapXmaxExclLock     uint16 = 0x0040
	HeapXmaxSharedLock   uint16 = 0x0080

	HeapXminCommitted uint16 = 0x0100
	HeapXminInvalid   uint16 = 0x0200
	// HeapXminFrozen 同时置 COMMITTED|INVALID，是冻结元组的提示位组合。
	HeapXminFrozen uint16 = HeapXminCommitted | HeapXminInvalid

	HeapXmaxCommitted uint16 = 0x0400
	HeapXmaxInvalid   uint16 = 0x0800
	// HeapXmaxIsMulti 表示 xmax 字段存放的是 MultiXactId 而非普通 XID。
	HeapXmaxIsMulti uint16 = 0x1000
	// HeapXmaxIsLocker 表示 xmax 仅为行锁持有者，而非删除者。
	HeapXmaxIsLocker uint16 = 0x2000

	// HeapXmaxShrLock 是共享锁模式位组合。
	HeapXmaxShrLock = HeapXmaxExclLock | HeapXmaxSharedLock
)

// Tuple 模拟一个堆元组的事务可见性相关头部。
type Tuple struct {
	// Xmin 是插入该元组的事务号；冻结后原地改写为 FrozenXid。
	Xmin XID
	// Xmax 在普通情况下是删除/锁定事务号；置 HeapXmaxIsMulti 时
	// 存放 MultiXactId。
	Xmax     XID
	Infomask uint16

	// Payload 承载任意业务数据，引擎不解读其内容。
	Payload any
}

// HasMask 报告是否置有全部给定标志位。
func (t *Tuple) HasMask(mask uint16) bool {
	return t.Infomask&mask == mask
}

// SetMask 置位给定标志。
func (t *Tuple) SetMask(mask uint16) {
	t.Infomask |= mask
}

// ClearMask 清除给定标志。
func (t *Tuple) ClearMask(mask uint16) {
	t.Infomask &^= mask
}

// IsXmaxMulti 报告 xmax 是否为 MultiXact 引用。
func (t *Tuple) IsXmaxMulti() bool {
	return t.HasMask(HeapXmaxIsMulti)
}

// XmaxIsValid 报告 xmax 是否仍是一个生效中的事务引用。
func (t *Tuple) XmaxIsValid() bool {
	return t.Xmax != InvalidXid && !t.HasMask(HeapXmaxInvalid)
}

// Page 模拟一个堆页，元组按指针存放，清理会原地收缩切片。
type Page struct {
	BlockNo uint32
	Tuples  []*Tuple
}
