package netsec

import "crypto/sha256"

// lockDirs 按目录序号顺序加锁，避免跨目录操作死锁。
func lockDirs(a, b *Directory) func() {
	if a == b {
		a.mu.Lock()
		return a.mu.Unlock
	}
	if a.serial < b.serial {
		a.mu.Lock()
		b.mu.Lock()
	} else {
		b.mu.Lock()
		a.mu.Lock()
	}
	return func() {
		a.mu.Unlock()
		b.mu.Unlock()
	}
}

// Rename 把 src 中的 srcName 迁移为 dst 中的 dstName。
// 跨目录时先解密源项，再按目标目录策略重新封装；
// 目标插入成功前源项保持不动，任何失败都不会移除源项。
func Rename(src *Directory, srcName string, dst *Directory, dstName string) error {
	srcCanonical, srcHash, err := src.prepare(srcName)
	if err != nil {
		return err
	}
	dstCanonical, dstHash, err := dst.prepare(dstName)
	if err != nil {
		return err
	}
	unlock := lockDirs(src, dst)
	defer unlock()

	srcEnt, err := src.findLocked(srcCanonical, srcHash)
	if err != nil {
		return err
	}
	if _, err := src.open(srcEnt); err != nil {
		return ErrIntegrity
	}

	exclude := map[*entry]struct{}{}
	if src == dst {
		exclude[srcEnt] = struct{}{}
	}
	if err := dst.conflictLocked(dstCanonical, dstHash, dstName, exclude); err != nil {
		return err
	}

	sealed, err := dst.seal(dst.current, dstName, dstHash, srcEnt.inodeID, srcEnt.inodeGen)
	if err != nil {
		return err
	}
	sealed.lookupKey = dst.host.lookupKey(dst.nameKey(dst.current), dstCanonical)

	// 先挂入目标，再摘除源项；此前任何失败源项均保持不动。
	dst.insertEntryLocked(sealed)
	src.removeEntryLocked(srcEnt)
	return nil
}

// Exchange 原子交换同一目录中两个名字，inode 身份随目录项保持不动。
func (d *Directory) Exchange(nameA, nameB string) error {
	canonA, hashA, err := d.prepare(nameA)
	if err != nil {
		return err
	}
	canonB, hashB, err := d.prepare(nameB)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	eA, err := d.findLocked(canonA, hashA)
	if err != nil {
		return err
	}
	eB, err := d.findLocked(canonB, hashB)
	if err != nil {
		return err
	}
	if eA == eB {
		return nil
	}
	rawA, err := d.open(eA)
	if err != nil {
		return ErrIntegrity
	}
	rawB, err := d.open(eB)
	if err != nil {
		return ErrIntegrity
	}

	// 交换后 eA 持有 rawB、eB 持有 rawA；规范名与查找键随之重算。
	newCanonA := Canonicalize(rawB, d.policy.CaseMode)
	newHashA := sha256.Sum256([]byte(newCanonA))
	newCanonB := Canonicalize(rawA, d.policy.CaseMode)
	newHashB := sha256.Sum256([]byte(newCanonB))

	exclude := map[*entry]struct{}{eA: {}, eB: {}}
	if err := d.conflictLocked(newCanonA, newHashA, rawB, exclude); err != nil {
		return err
	}
	if err := d.conflictLocked(newCanonB, newHashB, rawA, exclude); err != nil {
		return err
	}

	sealedA, err := d.seal(d.current, rawB, newHashA, eA.inodeID, eA.inodeGen)
	if err != nil {
		return err
	}
	sealedB, err := d.seal(d.current, rawA, newHashB, eB.inodeID, eB.inodeGen)
	if err != nil {
		return err
	}
	sealedA.lookupKey = d.host.lookupKey(d.nameKey(d.current), newCanonA)
	sealedB.lookupKey = d.host.lookupKey(d.nameKey(d.current), newCanonB)

	d.removeEntryLocked(eA)
	d.removeEntryLocked(eB)
	d.insertEntryLocked(sealedA)
	d.insertEntryLocked(sealedB)
	return nil
}
