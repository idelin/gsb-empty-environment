package netsec

// DefaultFamilyTable returns the open and rename families named in the policy
// model. Callers that also care about unlink, chmod, xattr, symlink, and
// similar calls should extend this table or supply their own.
func DefaultFamilyTable() FamilyTable {
	openAccess := AccessRead | AccessWrite | AccessCreate | AccessTruncate
	renameSource := AccessRename | AccessDelete
	renameDestination := AccessRename | AccessWrite | AccessCreate

	return FamilyTable{
		{
			Name: "open",
			Paths: []PathSpec{{
				ArgIndex:   0,
				DirFDIndex: -1,
				Access:     openAccess,
				Role:       "path",
			}},
		},
		{
			Name: "openat",
			Paths: []PathSpec{{
				ArgIndex:   1,
				DirFDIndex: 0,
				Access:     openAccess,
				Role:       "path",
			}},
		},
		{
			Name: "openat2",
			Paths: []PathSpec{{
				ArgIndex:   1,
				DirFDIndex: 0,
				Access:     openAccess,
				Role:       "path",
			}},
		},
		{
			Name: "creat",
			Paths: []PathSpec{{
				ArgIndex:   0,
				DirFDIndex: -1,
				Access:     AccessWrite | AccessCreate | AccessTruncate,
				Role:       "path",
			}},
		},
		{
			Name: "truncate",
			Paths: []PathSpec{{
				ArgIndex:   0,
				DirFDIndex: -1,
				Access:     AccessWrite | AccessTruncate,
				Role:       "path",
			}},
		},
		{
			Name: "rename",
			Paths: []PathSpec{
				{
					ArgIndex:   0,
					DirFDIndex: -1,
					Access:     renameSource,
					Role:       "source",
				},
				{
					ArgIndex:   1,
					DirFDIndex: -1,
					Access:     renameDestination,
					Role:       "destination",
				},
			},
		},
		{
			Name: "renameat",
			Paths: []PathSpec{
				{
					ArgIndex:   1,
					DirFDIndex: 0,
					Access:     renameSource,
					Role:       "source",
				},
				{
					ArgIndex:   3,
					DirFDIndex: 2,
					Access:     renameDestination,
					Role:       "destination",
				},
			},
		},
		{
			Name: "renameat2",
			Paths: []PathSpec{
				{
					ArgIndex:   1,
					DirFDIndex: 0,
					Access:     renameSource,
					Role:       "source",
				},
				{
					ArgIndex:   3,
					DirFDIndex: 2,
					Access:     renameDestination,
					Role:       "destination",
				},
			},
		},
	}
}
