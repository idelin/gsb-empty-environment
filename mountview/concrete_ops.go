package mountview

import "fmt"

func (e *Engine) applyConcrete(en *LogEntry) error {
	if err := e.verifier.VerifyEntry(en); err != nil {
		return err
	}
	if en.Seq <= e.lastSeq {
		return fmt.Errorf("mountview: log seq %d out of order (last %d)", en.Seq, e.lastSeq)
	}
	cp, err := CleanPath(en.Path)
	if err != nil {
		return err
	}
	en.Path = cp
	if en.NewPath != "" {
		np, err := CleanPath(en.NewPath)
		if err != nil {
			return err
		}
		en.NewPath = np
	}
	if en.Target != "" {
		tp, err := CleanPath(en.Target)
		if err != nil {
			return err
		}
		en.Target = tp
	}
	switch en.Op {
	case OpBindMount:
		e.concreteBind(en)
	case OpUnmount:
		e.concreteUnmount(en)
	case OpMove:
		if err := e.concreteMove(en); err != nil {
			return err
		}
	case OpSymlink:
		e.concreteSymlink(en)
	case OpHardlink:
		if err := e.concreteHardlink(en); err != nil {
			return err
		}
	case OpSlice:
		if err := e.concreteSlice(en); err != nil {
			return err
		}
	case OpChmod:
		if err := e.concreteChmod(en); err != nil {
			return err
		}
	case OpPropagation:
		if err := e.concretePropagation(en); err != nil {
			return err
		}
	default:
		return fmt.Errorf("mountview: unknown op %q", en.Op)
	}
	e.lastSeq = en.Seq
	return nil
}

func (e *Engine) concreteBind(en *LogEntry) {
	g := e.group(en.Group)
	g.present = true
	g.mayAbsent = false
	g.exactObject = en.Object
	g.exactInter = en.Interval
	g.exactPerm = en.Perm
	g.exactProp = en.Prop
	g.exactDomain = en.Domain
	cell := e.cell(en.Path)
	cell.present = true
	cell.mayAbsent = false
	cell.symlink = false
	cell.exactTarget = ""
	cell.mayTargets = setStr{}
	cell.hasGroup = true
	cell.exactGroup = en.Group
	cell.mayGroups = newSetG()
	cell.mayGroups.add(en.Group)
}

func (e *Engine) concreteUnmount(en *LogEntry) {
	cell, ok := e.paths[en.Path]
	if !ok {
		return
	}
	if cell.hasGroup {
		if g := e.groups[cell.exactGroup]; g != nil && !aliasReachableElsewhere(e, g.id, en.Path) {
			g.present = false
		}
	}
	delete(e.paths, en.Path)
}

func aliasReachableElsewhere(e *Engine, id GroupID, except string) bool {
	for p, cell := range e.paths {
		if p == except {
			continue
		}
		if cell.hasGroup && cell.exactGroup == id {
			return true
		}
		if cell.mayGroups != nil && (cell.mayGroups.m[id] || cell.mayGroups.any) {
			return true
		}
	}
	return false
}

func (e *Engine) concreteMove(en *LogEntry) error {
	src, ok := e.paths[en.Path]
	if !ok || !src.present {
		return fmt.Errorf("mountview: move at seq %d references absent path %s", en.Seq, en.Path)
	}
	if dst := e.paths[en.NewPath]; dst != nil && dst.present {
		return fmt.Errorf("mountview: move at seq %d targets occupied path %s", en.Seq, en.NewPath)
	}
	src.path = en.NewPath
	delete(e.paths, en.Path)
	e.paths[en.NewPath] = src
	return nil
}

func (e *Engine) concreteSymlink(en *LogEntry) {
	cell := e.cell(en.Path)
	cell.present = true
	cell.mayAbsent = false
	cell.symlink = true
	cell.exactTarget = en.Target
	cell.mayTargets = setStr{en.Target: true}
	cell.hasGroup = false
	cell.exactGroup = 0
	cell.mayGroups = newSetG()
}

func (e *Engine) concreteHardlink(en *LogEntry) error {
	src, ok := e.paths[en.Path]
	if !ok || !src.present || !src.hasGroup {
		return fmt.Errorf("mountview: hardlink at seq %d needs existing content handle at %s", en.Seq, en.Path)
	}
	if en.Group != 0 && en.Group != src.exactGroup {
		return fmt.Errorf("mountview: hardlink group mismatch at seq %d", en.Seq)
	}
	dst := e.cell(en.NewPath)
	if dst.present {
		return fmt.Errorf("mountview: hardlink at seq %d targets occupied path %s", en.Seq, en.NewPath)
	}
	dst.present = true
	dst.mayAbsent = false
	dst.symlink = false
	dst.exactTarget = ""
	dst.mayTargets = setStr{}
	dst.hasGroup = true
	dst.exactGroup = src.exactGroup
	dst.mayGroups = newSetG()
	dst.mayGroups.add(src.exactGroup)
	return nil
}

func (e *Engine) concreteSlice(en *LogEntry) error {
	cell, ok := e.paths[en.Path]
	if !ok || !cell.present || !cell.hasGroup {
		return fmt.Errorf("mountview: slice at seq %d references absent content handle %s", en.Seq, en.Path)
	}
	g := e.group(cell.exactGroup)
	g.exactInter = en.Interval
	g.mayInters = setI{}
	return nil
}

func (e *Engine) concreteChmod(en *LogEntry) error {
	cell, ok := e.paths[en.Path]
	if !ok || !cell.present || !cell.hasGroup {
		return fmt.Errorf("mountview: chmod at seq %d references absent content handle %s", en.Seq, en.Path)
	}
	g := e.group(cell.exactGroup)
	g.exactPerm = en.Perm
	g.mayPerms = setP{}
	return nil
}

func (e *Engine) concretePropagation(en *LogEntry) error {
	cell, ok := e.paths[en.Path]
	if !ok || !cell.present || !cell.hasGroup {
		return fmt.Errorf("mountview: propagation at seq %d references absent content handle %s", en.Seq, en.Path)
	}
	g := e.group(cell.exactGroup)
	g.exactProp = en.Prop
	g.exactDomain = en.Domain
	g.mayProps = map[Propagation]bool{}
	g.mayDomains = map[DomainID]bool{}
	return nil
}
