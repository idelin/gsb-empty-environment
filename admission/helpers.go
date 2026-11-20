package admission

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// findPerm 在权限集合中定位某卷的权限项，未命中返回 false。
func findPerm(perms []VolumePerm, volume VolumeID) (VolumePerm, bool) {
	for _, perm := range perms {
		if perm.Volume == volume {
			return perm, true
		}
	}
	return VolumePerm{}, false
}

// volumesSubset 报告 want 中每个卷的权限是否都被 have 完整覆盖。
// 只可转授自己拥有的读/写权限，不能放大，也不能夹带未持有的卷。
func volumesSubset(want, have []VolumePerm) bool {
	for _, wanted := range want {
		held, ok := findPerm(have, wanted.Volume)
		if !ok || !held.Allows(wanted) {
			return false
		}
	}
	return true
}

// claimIDByDigest 用声明摘要反查声明 ID；找不到返回空串。
func claimIDByDigest(policy Policy, digest Digest) string {
	for id, claim := range policy.Tree.Claims {
		d, err := claim.Digest()
		if err != nil {
			continue
		}
		if d == digest {
			return id
		}
	}
	return ""
}

// sameVolumes 比较两个权限集合是否逐项一致（顺序无关）。
func sameVolumes(a, b []VolumePerm) bool {
	if len(a) != len(b) {
		return false
	}
	for _, perm := range a {
		other, ok := findPerm(b, perm.Volume)
		if !ok || other.Read != perm.Read || other.Write != perm.Write {
			return false
		}
	}
	return true
}
