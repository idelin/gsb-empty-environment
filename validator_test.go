package netsec

import "testing"

var sensitivePrefixes = []string{"/proc", "/sys"}

func inode(n uint64) InodeID { return InodeID{Device: 7, Inode: n} }

func TestSensitivePathClone(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		flags    OpenTreeFlag
		wantCode ViolationCode
	}{
		{
			name:     "clone under /proc is rejected",
			path:     "/proc/1/root",
			flags:    OpenTreeClone | OpenTreeRecursive,
			wantCode: ViolationSensitiveClone,
		},
		{
			name:     "clone of /sys itself is rejected",
			path:     "/sys/",
			flags:    OpenTreeClone,
			wantCode: ViolationSensitiveClone,
		},
		{
			name:     "clone under host root is rejected",
			path:     "/var/lib/private",
			flags:    OpenTreeClone | OpenTreeRecursive,
			wantCode: ViolationSensitiveClone,
		},
		{
			name:     "component boundary: /procure is not /proc",
			path:     "/procure/bin",
			flags:    OpenTreeClone,
			wantCode: "",
		},
		{
			name:     "non-clone open_tree under sensitive prefix is allowed",
			path:     "/proc/self/ns/mnt",
			flags:    OpenTreeRecursive,
			wantCode: "",
		},
		{
			name:     "cleaned path still matched",
			path:     "/proc/../proc/1",
			flags:    OpenTreeClone,
			wantCode: ViolationSensitiveClone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := []Event{
				OpenTreeEvent{Path: tt.path, Flags: tt.flags, Descriptor: 3},
			}
			prefixes := sensitivePrefixes
			if tt.name == "clone under host root is rejected" {
				prefixes = []string{"/"}
			}
			violation := ValidateSequence(prefixes, events)
			assertCode(t, violation, tt.wantCode)
		})
	}
}

func TestTargetReplacementTOCTOU(t *testing.T) {
	base := []Event{
		OpenTreeEvent{
			Path:       "/containers/c1/rootfs/mnt",
			Flags:      OpenTreeClone | OpenTreeRecursive,
			Descriptor: 4,
		},
	}

	t.Run("inode mismatch rejects", func(t *testing.T) {
		events := append(append([]Event{}, base...), MoveMountEvent{
			SourceDescriptor: 4,
			TargetPath:       "/containers/c1/rootfs/new",
			Flags:            MoveMountFEmpty,
			CheckInode:       inode(100),
			UseInode:         inode(999),
		})
		violation := ValidateSequence(sensitivePrefixes, events)
		assertCode(t, violation, ViolationInodeMismatch)
	})

	t.Run("same inode passes", func(t *testing.T) {
		events := append(append([]Event{}, base...), MoveMountEvent{
			SourceDescriptor: 4,
			TargetPath:       "/containers/c1/rootfs/new",
			CheckInode:       inode(100),
			UseInode:         inode(100),
		})
		if violation := ValidateSequence(sensitivePrefixes, events); violation != nil {
			t.Fatalf("unexpected violation: %v", violation)
		}
	})
}

func TestDescriptorReuseAndProvenance(t *testing.T) {
	t.Run("clone descriptor used twice is rejected", func(t *testing.T) {
		events := []Event{
			OpenTreeEvent{Path: "/scratch/tree", Flags: OpenTreeClone, Descriptor: 5},
			MoveMountEvent{SourceDescriptor: 5, TargetPath: "/a", CheckInode: inode(1), UseInode: inode(1)},
			MoveMountEvent{SourceDescriptor: 5, TargetPath: "/b", CheckInode: inode(2), UseInode: inode(2)},
		}
		violation := ValidateSequence(sensitivePrefixes, events)
		assertCode(t, violation, ViolationDescriptorReused)
	})

	t.Run("fsmount descriptor used twice is rejected", func(t *testing.T) {
		events := []Event{
			FSMountEvent{Descriptor: 6},
			MoveMountEvent{SourceDescriptor: 6, TargetPath: "/a", CheckInode: inode(1), UseInode: inode(1)},
			MoveMountEvent{SourceDescriptor: 6, TargetPath: "/b", CheckInode: inode(2), UseInode: inode(2)},
		}
		violation := ValidateSequence(sensitivePrefixes, events)
		assertCode(t, violation, ViolationDescriptorReused)
	})

	t.Run("descriptor never produced in sequence is rejected", func(t *testing.T) {
		events := []Event{
			MoveMountEvent{SourceDescriptor: 42, TargetPath: "/a", CheckInode: inode(1), UseInode: inode(1)},
		}
		violation := ValidateSequence(sensitivePrefixes, events)
		assertCode(t, violation, ViolationUnknownDescriptor)
	})

	t.Run("plain open_tree handle is not a valid move source", func(t *testing.T) {
		events := []Event{
			OpenTreeEvent{Path: "/scratch/tree", Flags: 0, Descriptor: 7},
			MoveMountEvent{SourceDescriptor: 7, TargetPath: "/a", CheckInode: inode(1), UseInode: inode(1)},
		}
		violation := ValidateSequence(sensitivePrefixes, events)
		assertCode(t, violation, ViolationInvalidSource)
	})
}

func TestSharedSubtree(t *testing.T) {
	clone := func() OpenTreeEvent {
		return OpenTreeEvent{
			Path:          "/scratch/shared-tree",
			Flags:         OpenTreeClone | OpenTreeRecursive,
			Descriptor:    8,
			CarriesShared: true,
		}
	}
	move := MoveMountEvent{
		SourceDescriptor: 8,
		TargetPath:       "/c/mnt",
		CheckInode:       inode(9),
		UseInode:         inode(9),
	}

	t.Run("shared subtree without remediating setattr is rejected", func(t *testing.T) {
		events := []Event{clone(), move}
		violation := ValidateSequence(sensitivePrefixes, events)
		assertCode(t, violation, ViolationSharedSubtree)
	})

	t.Run("recursive private clears the shared risk", func(t *testing.T) {
		events := []Event{
			clone(),
			MountSetattrEvent{Descriptor: 8, Recursive: true, Propagation: PropagationPrivate},
			move,
		}
		if violation := ValidateSequence(sensitivePrefixes, events); violation != nil {
			t.Fatalf("unexpected violation: %v", violation)
		}
	})

	t.Run("recursive slave clears the shared risk", func(t *testing.T) {
		events := []Event{
			clone(),
			MountSetattrEvent{Descriptor: 8, Recursive: true, Propagation: PropagationSlave},
			move,
		}
		if violation := ValidateSequence(sensitivePrefixes, events); violation != nil {
			t.Fatalf("unexpected violation: %v", violation)
		}
	})

	t.Run("non-recursive private does not reach nested mounts", func(t *testing.T) {
		events := []Event{
			clone(),
			MountSetattrEvent{Descriptor: 8, Recursive: false, Propagation: PropagationPrivate},
			move,
		}
		violation := ValidateSequence(sensitivePrefixes, events)
		assertCode(t, violation, ViolationSharedSubtree)
	})

	t.Run("fsmount never carries a shared subtree", func(t *testing.T) {
		events := []Event{
			FSMountEvent{Descriptor: 9},
			MoveMountEvent{SourceDescriptor: 9, TargetPath: "/c/mnt", CheckInode: inode(9), UseInode: inode(9)},
		}
		if violation := ValidateSequence(sensitivePrefixes, events); violation != nil {
			t.Fatalf("unexpected violation: %v", violation)
		}
	})
}

func TestClosedDescriptor(t *testing.T) {
	t.Run("move_mount after close is rejected", func(t *testing.T) {
		events := []Event{
			OpenTreeEvent{Path: "/scratch/tree", Flags: OpenTreeClone, Descriptor: 10},
			CloseEvent{Descriptor: 10},
			MoveMountEvent{SourceDescriptor: 10, TargetPath: "/a", CheckInode: inode(1), UseInode: inode(1)},
		}
		violation := ValidateSequence(sensitivePrefixes, events)
		assertCode(t, violation, ViolationDescriptorClosed)
	})

	t.Run("mount_setattr after close is rejected", func(t *testing.T) {
		events := []Event{
			OpenTreeEvent{Path: "/scratch/tree", Flags: OpenTreeClone, Descriptor: 11},
			CloseEvent{Descriptor: 11},
			MountSetattrEvent{Descriptor: 11, Recursive: true, Propagation: PropagationPrivate},
		}
		violation := ValidateSequence(sensitivePrefixes, events)
		assertCode(t, violation, ViolationDescriptorClosed)
	})

	t.Run("descriptor number reused after close is a fresh state", func(t *testing.T) {
		events := []Event{
			OpenTreeEvent{Path: "/scratch/old", Flags: OpenTreeClone, Descriptor: 12},
			CloseEvent{Descriptor: 12},
			OpenTreeEvent{Path: "/scratch/new", Flags: OpenTreeClone, Descriptor: 12},
			MoveMountEvent{SourceDescriptor: 12, TargetPath: "/a", CheckInode: inode(1), UseInode: inode(1)},
		}
		if violation := ValidateSequence(sensitivePrefixes, events); violation != nil {
			t.Fatalf("unexpected violation: %v", violation)
		}
	})
}

func TestRootPrefixMatchesEveryAbsolutePath(t *testing.T) {
	validator := NewValidator([]string{"/"})
	violation := validator.Feed(0, OpenTreeEvent{
		Path:       "/anything/below/root",
		Flags:      OpenTreeClone,
		Descriptor: 1,
	})
	assertCode(t, violation, ViolationSensitiveClone)
}

func TestHappyPathFullSequence(t *testing.T) {
	events := []Event{
		FSMountEvent{Descriptor: 20},
		OpenTreeEvent{
			Path:          "/scratch/tree",
			Flags:         OpenTreeClone | OpenTreeRecursive,
			Descriptor:    21,
			CarriesShared: true,
		},
		MountSetattrEvent{Descriptor: 21, Recursive: true, Propagation: PropagationSlave},
		MoveMountEvent{SourceDescriptor: 21, TargetPath: "/c/mnt1", CheckInode: inode(30), UseInode: inode(30)},
		MoveMountEvent{SourceDescriptor: 20, TargetPath: "/c/mnt2", CheckInode: inode(31), UseInode: inode(31)},
	}
	if violation := ValidateSequence(sensitivePrefixes, events); violation != nil {
		t.Fatalf("unexpected violation: %v", violation)
	}
}

func TestIncrementalFeedParityAndReset(t *testing.T) {
	events := []Event{
		OpenTreeEvent{Path: "/proc/1", Flags: OpenTreeClone, Descriptor: 1},
	}

	validator := NewValidator(sensitivePrefixes)
	for index, event := range events {
		violation := validator.Feed(index, event)
		assertCode(t, violation, ViolationSensitiveClone)
	}

	validator.Reset()
	good := []Event{
		FSMountEvent{Descriptor: 1},
		MoveMountEvent{SourceDescriptor: 1, TargetPath: "/a", CheckInode: inode(1), UseInode: inode(1)},
	}
	for index, event := range good {
		if violation := validator.Feed(index, event); violation != nil {
			t.Fatalf("unexpected violation after reset: %v", violation)
		}
	}
}

func TestAsViolation(t *testing.T) {
	events := []Event{
		MoveMountEvent{SourceDescriptor: 1, CheckInode: inode(1), UseInode: inode(2)},
	}
	err := error(ValidateSequence(nil, events))
	violation, ok := AsViolation(err)
	if !ok {
		t.Fatal("errors.As failed to extract *Violation")
	}
	if violation.Code != ViolationUnknownDescriptor {
		t.Fatalf("got code %q, want %q", violation.Code, ViolationUnknownDescriptor)
	}
	if _, ok := AsViolation(nil); ok {
		t.Fatal("nil error must not produce a violation")
	}
}

type bogusEvent struct{}

func (bogusEvent) isEvent() {}

func TestUnknownAndIgnoredEvents(t *testing.T) {
	t.Run("unknown event type is rejected", func(t *testing.T) {
		validator := NewValidator(sensitivePrefixes)
		violation := validator.Feed(0, bogusEvent{})
		assertCode(t, violation, ViolationUnknownDescriptor)

		var err error = violation
		if err.Error() == "" || err.Error() != violation.Detail {
			t.Fatalf("Violation does not behave as an error: %q", err.Error())
		}
	})

	t.Run("closing an untracked descriptor is ignored", func(t *testing.T) {
		events := []Event{
			CloseEvent{Descriptor: 99},
			FSMountEvent{Descriptor: 1},
			MoveMountEvent{SourceDescriptor: 1, TargetPath: "/a", CheckInode: inode(1), UseInode: inode(1)},
		}
		if violation := ValidateSequence(sensitivePrefixes, events); violation != nil {
			t.Fatalf("unexpected violation: %v", violation)
		}
	})

	t.Run("empty or duplicate prefixes are tolerated", func(t *testing.T) {
		validator := NewValidator([]string{"", "/proc", "/proc", "proc"})
		if len(validator.prefixes) != 2 || validator.prefixes[0] != "/proc" || validator.prefixes[1] != "proc" {
			t.Fatalf("prefixes not deduplicated/cleaned: %#v", validator.prefixes)
		}
	})
}

func assertCode(t *testing.T, violation *Violation, want ViolationCode) {
	t.Helper()
	if want == "" {
		if violation != nil {
			t.Fatalf("unexpected violation %q: %v", violation.Code, violation)
		}
		return
	}
	if violation == nil {
		t.Fatalf("expected violation %q, got none", want)
	}
	if violation.Code != want {
		t.Fatalf("got violation %q, want %q", violation.Code, want)
	}
}
