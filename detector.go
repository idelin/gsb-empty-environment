package taskpkg

import (
	"errors"
	"sort"
)

// Analyze 对偏序事件输入执行令牌窃取—横向移动检测，返回三值结论与
// 支撑结论的三段证据。输入非法（环、未知引用、超量事件等）时返回错误。
func Analyze(in Input) (Report, error) {
	if len(in.Events) > maxEvents {
		return Report{}, errors.New("too many events: detector accepts at most 20 events")
	}
	subjects, devices, err := buildLineages(in)
	if err != nil {
		return Report{}, err
	}
	g, err := buildOrderGraph(in.Events, in.Revocations)
	if err != nil {
		return Report{}, err
	}
	if err := validateEvents(in.Events); err != nil {
		return Report{}, err
	}

	var reads, auths, ops []Event
	for i := range in.Events {
		switch in.Events[i].Kind {
		case KindTokenRead:
			reads = append(reads, in.Events[i])
		case KindTokenAuth:
			auths = append(auths, in.Events[i])
		case KindProtectedOp:
			ops = append(ops, in.Events[i])
		default:
			return Report{}, errors.New("event " + in.Events[i].ID + " has unknown kind")
		}
	}

	var certain, possible []Evidence
	for r := range reads {
		read := reads[r]
		for a := range auths {
			auth := auths[a]
			if !sameTokenFlow(read, auth, subjects, devices) {
				continue
			}
			for o := range ops {
				op := ops[o]
				if !boundToAuth(auth, op) {
					continue
				}
				for v := range in.Revocations {
					rev := in.Revocations[v]
					if rev.Token != read.Token {
						continue
					}
					required := requiredEdges(read, auth, op, rev)
					// 补边成环意味着该证据与已知偏序矛盾，任何拓扑序都不成立。
					if !g.feasibleWith(required) {
						continue
					}
					ev := Evidence{
						ReadEventID:      read.ID,
						AuthEventID:      auth.ID,
						OperationEventID: op.ID,
						RevocationID:     rev.ID,
						MissingEdges:     missingEdges(g, required),
					}
					if len(ev.MissingEdges) == 0 {
						certain = append(certain, ev)
					} else {
						possible = append(possible, ev)
					}
				}
			}
		}
	}

	sortEvidence(certain)
	sortEvidence(possible)
	report := Report{Certain: certain, Possible: possible}
	switch {
	case len(certain) > 0:
		report.Verdict = VerdictCertain
	case len(possible) > 0:
		report.Verdict = VerdictPossible
	default:
		report.Verdict = VerdictNotEstablished
	}
	return report, nil
}

// buildLineages 构建主体与设备两张受信任派生图。
func buildLineages(in Input) (*lineage, *lineage, error) {
	subjects, err := buildLineage(in.SubjectDerives)
	if err != nil {
		return nil, nil, errors.New("subject derivation: " + err.Error())
	}
	devices, err := buildLineage(in.DeviceDerives)
	if err != nil {
		return nil, nil, errors.New("device derivation: " + err.Error())
	}
	return subjects, devices, nil
}

// validateEvents 检查业务事件自身的字段完整性与操作会话绑定字段。
func validateEvents(events []Event) error {
	for i := range events {
		e := events[i]
		if e.Subject.Name == "" || e.Device.Name == "" {
			return errors.New("event " + e.ID + " missing subject or device identity")
		}
		if e.Kind == KindProtectedOp && e.Session == "" {
			return errors.New("protected operation " + e.ID + " missing session binding")
		}
		if e.Kind == KindTokenAuth && e.Session == "" {
			return errors.New("authentication " + e.ID + " missing session id")
		}
	}
	return nil
}

// sameTokenFlow 判断读取事件到认证事件是否构成可信的同一令牌流动：
// 令牌身份三元组全等，且认证主体/设备分别沿受信任派生关系自读取
// 主体/设备可达。任一谱系不可达即拒绝拼接。
func sameTokenFlow(read, auth Event, subjects, devices *lineage) bool {
	if read.Token != auth.Token {
		return false
	}
	if !subjects.reaches(read.Subject, auth.Subject) {
		return false
	}
	if !devices.reaches(read.Device, auth.Device) {
		return false
	}
	return true
}

// boundToAuth 判断受保护操作是否绑定到该次认证：同主体实例、同设备
// 实例、同会话标识。仅令牌相同不足以绑定，防止跨会话拼接。
func boundToAuth(auth, op Event) bool {
	return op.Token == auth.Token &&
		op.Subject == auth.Subject &&
		op.Device == auth.Device &&
		op.Session == auth.Session
}

// requiredEdges 返回一条证据成立所需的全部严格先后因果边：
// 读取先于认证（窃取后使用）、认证先于操作、撤销先于操作（撤销后操作）。
func requiredEdges(read, auth, op Event, rev Revocation) []OrderedPair {
	return []OrderedPair{
		{From: read.ID, To: auth.ID},
		{From: auth.ID, To: op.ID},
		{From: rev.ID, To: op.ID},
	}
}

// missingEdges 过滤出尚未被输入偏序蕴含的所需因果边。
func missingEdges(g *orderGraph, required []OrderedPair) []OrderedPair {
	var missing []OrderedPair
	for _, p := range required {
		from := g.index[p.From]
		to := g.index[p.To]
		if !g.before(from, to) {
			missing = append(missing, p)
		}
	}
	return missing
}

// sortEvidence 让输出顺序确定，便于宿主比较与测试。
func sortEvidence(evs []Evidence) {
	sort.Slice(evs, func(i, j int) bool {
		a, b := evs[i], evs[j]
		for _, key := range [][2]string{
			{a.ReadEventID, b.ReadEventID},
			{a.AuthEventID, b.AuthEventID},
			{a.OperationEventID, b.OperationEventID},
			{a.RevocationID, b.RevocationID},
		} {
			if key[0] != key[1] {
				return key[0] < key[1]
			}
		}
		return false
	})
}
