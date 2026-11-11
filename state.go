package netsec

import "sort"

// flowState 是分析内部使用的抽象状态。
//
// top 为真表示格顶（不可达 / 无任何路径到达）。其余字段只在 top 为假时
// 有意义：versions 记录每个对象的共同当前版本，creds 记录所有到达路径
// 共同持有的凭据（must 集合）。
type flowState struct {
	top      bool
	versions map[string]string
	creds    map[Credential]struct{}
}

// topState 返回格顶。
func topState() flowState {
	return flowState{top: true}
}

// initialState 构造入口状态：所有出现过的对象取初始版本，无任何凭据。
func initialState(objects []string) flowState {
	versions := make(map[string]string, len(objects))
	for _, object := range objects {
		versions[object] = InitialVersion
	}
	return flowState{
		versions: versions,
		creds:    make(map[Credential]struct{}),
	}
}

// clone 深拷贝状态。
func (s flowState) clone() flowState {
	if s.top {
		return topState()
	}
	out := flowState{
		versions: make(map[string]string, len(s.versions)),
		creds:    make(map[Credential]struct{}, len(s.creds)),
	}
	for object, version := range s.versions {
		out.versions[object] = version
	}
	for cred := range s.creds {
		out.creds[cred] = struct{}{}
	}
	return out
}

// equal 判断两个状态是否相等，是不动点停机判据。
func (s flowState) equal(other flowState) bool {
	if s.top != other.top {
		return false
	}
	if s.top {
		return true
	}
	if len(s.versions) != len(other.versions) || len(s.creds) != len(other.creds) {
		return false
	}
	for object, version := range s.versions {
		if other.versions[object] != version {
			return false
		}
	}
	for cred := range s.creds {
		if _, ok := other.creds[cred]; !ok {
			return false
		}
	}
	return true
}

// mergeStates 计算合流：只保留所有前驱共同保证的事实。
//
// 任一前驱为格顶（不可达）时忽略；全部为格顶时结果为格顶。
// 同一对象版本不一致时退化为 UnknownVersion；凭据取交集。
func mergeStates(preds []flowState) flowState {
	merged := topState()
	for _, pred := range preds {
		if pred.top {
			continue
		}
		if merged.top {
			merged = pred.clone()
			continue
		}
		for object, version := range merged.versions {
			predVersion, ok := pred.versions[object]
			if !ok || predVersion != version {
				merged.versions[object] = UnknownVersion
			}
		}
		for cred := range merged.creds {
			if _, ok := pred.creds[cred]; !ok {
				delete(merged.creds, cred)
			}
		}
	}
	return merged
}

// transfer 按节点语义做前向迁移。
func transfer(in flowState, node Node) flowState {
	if in.top {
		return topState()
	}
	out := in.clone()

	switch node.Kind {
	case KindPlain, KindSensitiveCall:
		// 敏感调用的判定发生在入口状态上，节点本身不改变状态。
		return out

	case KindAuthCheck:
		version, ok := out.versions[node.Object]
		if !ok {
			// 图上出现过但未登记版本的对象，按初始版本处理（validate
			// 已保证所有对象预登记，此分支仅作防御）。
			version = InitialVersion
			out.versions[node.Object] = version
		}
		if version != UnknownVersion {
			out.creds[Credential{
				Subject: node.Subject,
				Object:  node.Object,
				Version: version,
			}] = struct{}{}
		}
		return out

	case KindModifyVersion:
		newVersion := ModifiedVersion + node.ID
		out.versions[node.Object] = newVersion
		// 旧版本凭据仍然是“曾对旧版本授权”的事实，但三元组中的版本
		// 已与当前版本不符，后续匹配自然失败，因此保留而不删除。
		return out

	case KindTransferCredential:
		// 凭据原样传递：主体、对象、版本均不变，授权范围不扩大。
		return out

	default:
		return out
	}
}

// stateKey 给 BFS 中的具体状态生成稳定键。
func stateKey(nodeID string, state flowState) string {
	objects := make([]string, 0, len(state.versions))
	for object := range state.versions {
		objects = append(objects, object)
	}
	sort.Strings(objects)

	creds := make([]Credential, 0, len(state.creds))
	for cred := range state.creds {
		creds = append(creds, cred)
	}
	sort.Slice(creds, func(i, j int) bool {
		if creds[i].Subject != creds[j].Subject {
			return creds[i].Subject < creds[j].Subject
		}
		if creds[i].Object != creds[j].Object {
			return creds[i].Object < creds[j].Object
		}
		return creds[i].Version < creds[j].Version
	})

	key := nodeID + "|"
	for _, object := range objects {
		key += object + "=" + state.versions[object] + ";"
	}
	key += "|"
	for _, cred := range creds {
		key += cred.Subject + "/" + cred.Object + "/" + cred.Version + ";"
	}
	return key
}
