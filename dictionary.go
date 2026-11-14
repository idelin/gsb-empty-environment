package netsec

type dicomAction uint8

const (
	actionKeep dicomAction = iota
	actionEmpty
	actionRemove
	actionUID
	actionDate
	actionSetPatientIdentityRemoved
	actionSetDeidentificationMethod
	actionSetTemporalModified
)

type dictEntry struct {
	vr     vr
	action dicomAction
}

var tagDictionary = map[Tag]dictEntry{
	// File meta information.
	{0x0002, 0x0000}: {vrUL, actionKeep},
	{0x0002, 0x0001}: {vrOB, actionKeep},
	{0x0002, 0x0002}: {vrUI, actionKeep},
	{0x0002, 0x0003}: {vrUI, actionUID},
	{0x0002, 0x0010}: {vrUI, actionKeep},
	{0x0002, 0x0012}: {vrUI, actionKeep},
	{0x0002, 0x0013}: {vrSH, actionKeep},
	{0x0002, 0x0016}: {vrAE, actionRemove},
	{0x0002, 0x0017}: {vrAE, actionRemove},
	{0x0002, 0x0018}: {vrAE, actionRemove},
	{0x0002, 0x0100}: {vrOB, actionRemove},
	{0x0002, 0x0102}: {vrUI, actionRemove},

	// General study, series, and instance identification.
	{0x0008, 0x0005}: {vrCS, actionKeep},
	{0x0008, 0x0012}: {vrDA, actionDate},
	{0x0008, 0x0013}: {vrTM, actionKeep},
	{0x0008, 0x0014}: {vrUI, actionUID},
	{0x0008, 0x0016}: {vrUI, actionKeep},
	{0x0008, 0x0018}: {vrUI, actionUID},
	{0x0008, 0x0020}: {vrDA, actionDate},
	{0x0008, 0x0021}: {vrDA, actionDate},
	{0x0008, 0x0022}: {vrDA, actionDate},
	{0x0008, 0x0023}: {vrDA, actionDate},
	{0x0008, 0x002A}: {vrDT, actionDate},
	{0x0008, 0x0030}: {vrTM, actionKeep},
	{0x0008, 0x0031}: {vrTM, actionKeep},
	{0x0008, 0x0032}: {vrTM, actionKeep},
	{0x0008, 0x0033}: {vrTM, actionKeep},
	{0x0008, 0x0050}: {vrSH, actionEmpty},
	{0x0008, 0x0052}: {vrCS, actionKeep},
	{0x0008, 0x0054}: {vrAE, actionEmpty},
	{0x0008, 0x0058}: {vrUI, actionUID},
	{0x0008, 0x0060}: {vrCS, actionKeep},
	{0x0008, 0x0061}: {vrCS, actionKeep},
	{0x0008, 0x0064}: {vrCS, actionKeep},
	{0x0008, 0x0068}: {vrCS, actionKeep},
	{0x0008, 0x0070}: {vrLO, actionEmpty},
	{0x0008, 0x0080}: {vrLO, actionEmpty},
	{0x0008, 0x0081}: {vrST, actionEmpty},
	{0x0008, 0x0090}: {vrPN, actionEmpty},
	{0x0008, 0x0092}: {vrST, actionEmpty},
	{0x0008, 0x0094}: {vrSH, actionEmpty},
	{0x0008, 0x0096}: {vrSQ, actionRemove},
	{0x0008, 0x010C}: {vrUI, actionKeep},
	{0x0008, 0x010D}: {vrUI, actionKeep},
	{0x0008, 0x0117}: {vrUI, actionKeep},
	{0x0008, 0x1010}: {vrSH, actionEmpty},
	{0x0008, 0x1030}: {vrLO, actionEmpty},
	{0x0008, 0x103E}: {vrLO, actionEmpty},
	{0x0008, 0x1040}: {vrLO, actionEmpty},
	{0x0008, 0x1048}: {vrPN, actionEmpty},
	{0x0008, 0x1049}: {vrSQ, actionRemove},
	{0x0008, 0x1050}: {vrPN, actionEmpty},
	{0x0008, 0x1052}: {vrSQ, actionRemove},
	{0x0008, 0x1060}: {vrPN, actionEmpty},
	{0x0008, 0x1062}: {vrSQ, actionRemove},
	{0x0008, 0x1070}: {vrPN, actionEmpty},
	{0x0008, 0x1072}: {vrSQ, actionRemove},
	{0x0008, 0x1080}: {vrLO, actionEmpty},
	{0x0008, 0x1090}: {vrLO, actionKeep},
	{0x0008, 0x1110}: {vrSQ, actionKeep},
	{0x0008, 0x1111}: {vrSQ, actionKeep},
	{0x0008, 0x1120}: {vrSQ, actionKeep},
	{0x0008, 0x1140}: {vrSQ, actionKeep},
	{0x0008, 0x114A}: {vrSQ, actionKeep},
	{0x0008, 0x1150}: {vrUI, actionKeep},
	{0x0008, 0x1155}: {vrUI, actionUID},
	{0x0008, 0x1195}: {vrUI, actionUID},
	{0x0008, 0x1190}: {vrUR, actionEmpty},
	{0x0008, 0x1192}: {vrUI, actionUID},
	{0x0008, 0x1198}: {vrSQ, actionKeep},
	{0x0008, 0x2111}: {vrST, actionEmpty},
	{0x0008, 0x3010}: {vrUI, actionUID},
	{0x0008, 0x4000}: {vrLT, actionEmpty},
	{0x0008, 0x9123}: {vrUI, actionKeep},

	// Patient module and patient-adjacent identifiers.
	{0x0010, 0x0010}: {vrPN, actionEmpty},
	{0x0010, 0x0020}: {vrLO, actionEmpty},
	{0x0010, 0x0021}: {vrLO, actionEmpty},
	{0x0010, 0x0022}: {vrCS, actionEmpty},
	{0x0010, 0x0024}: {vrSQ, actionRemove},
	{0x0010, 0x0026}: {vrSQ, actionRemove},
	{0x0010, 0x0027}: {vrSQ, actionRemove},
	{0x0010, 0x0028}: {vrUS, actionKeep},
	{0x0010, 0x0030}: {vrDA, actionDate},
	{0x0010, 0x0032}: {vrTM, actionEmpty},
	{0x0010, 0x0033}: {vrLO, actionEmpty},
	{0x0010, 0x0034}: {vrLO, actionEmpty},
	{0x0010, 0x0035}: {vrCS, actionEmpty},
	{0x0010, 0x0040}: {vrCS, actionEmpty},
	{0x0010, 0x1000}: {vrLO, actionEmpty},
	{0x0010, 0x1001}: {vrPN, actionEmpty},
	{0x0010, 0x1002}: {vrSQ, actionRemove},
	{0x0010, 0x1005}: {vrPN, actionEmpty},
	{0x0010, 0x1010}: {vrAS, actionEmpty},
	{0x0010, 0x1020}: {vrDS, actionEmpty},
	{0x0010, 0x1030}: {vrDS, actionEmpty},
	{0x0010, 0x1040}: {vrLO, actionEmpty},
	{0x0010, 0x1050}: {vrLO, actionEmpty},
	{0x0010, 0x1060}: {vrPN, actionEmpty},
	{0x0010, 0x1080}: {vrLO, actionEmpty},
	{0x0010, 0x1081}: {vrLO, actionEmpty},
	{0x0010, 0x1090}: {vrLO, actionEmpty},
	{0x0010, 0x1100}: {vrSQ, actionRemove},
	{0x0010, 0x2000}: {vrLT, actionEmpty},
	{0x0010, 0x2110}: {vrLT, actionEmpty},
	{0x0010, 0x2150}: {vrLO, actionEmpty},
	{0x0010, 0x2152}: {vrLO, actionEmpty},
	{0x0010, 0x2154}: {vrSH, actionEmpty},
	{0x0010, 0x2155}: {vrLT, actionEmpty},
	{0x0010, 0x2160}: {vrSH, actionEmpty},
	{0x0010, 0x2180}: {vrSH, actionEmpty},
	{0x0010, 0x21A0}: {vrCS, actionEmpty},
	{0x0010, 0x21B0}: {vrLT, actionEmpty},
	{0x0010, 0x21C0}: {vrUS, actionEmpty},
	{0x0010, 0x21D0}: {vrDA, actionEmpty},
	{0x0010, 0x2202}: {vrSQ, actionRemove},
	{0x0010, 0x2292}: {vrLO, actionEmpty},
	{0x0010, 0x2297}: {vrPN, actionEmpty},
	{0x0010, 0x2298}: {vrCS, actionEmpty},
	{0x0010, 0x2299}: {vrSQ, actionRemove},
	{0x0010, 0x22A0}: {vrLO, actionEmpty},
	{0x0010, 0x22A1}: {vrLO, actionEmpty},
	{0x0010, 0x22A2}: {vrLT, actionEmpty},
	{0x0010, 0x4000}: {vrLT, actionEmpty},

	// Clinical trial and de-identification bookkeeping.
	{0x0012, 0x0010}: {vrLO, actionEmpty},
	{0x0012, 0x0020}: {vrLO, actionEmpty},
	{0x0012, 0x0021}: {vrLO, actionEmpty},
	{0x0012, 0x0030}: {vrLO, actionEmpty},
	{0x0012, 0x0031}: {vrLO, actionEmpty},
	{0x0012, 0x0040}: {vrLO, actionEmpty},
	{0x0012, 0x0042}: {vrLO, actionEmpty},
	{0x0012, 0x0050}: {vrLO, actionEmpty},
	{0x0012, 0x0051}: {vrLO, actionEmpty},
	{0x0012, 0x0071}: {vrLO, actionEmpty},
	{0x0012, 0x0072}: {vrLO, actionEmpty},
	{0x0012, 0x0081}: {vrLO, actionEmpty},
	{0x0012, 0x0082}: {vrLO, actionEmpty},
	{0x0012, 0x0060}: {vrLO, actionEmpty},
	{0x0012, 0x0062}: {vrCS, actionSetPatientIdentityRemoved},
	{0x0012, 0x0063}: {vrLO, actionSetDeidentificationMethod},
	{0x0012, 0x0064}: {vrSQ, actionRemove},

	// Equipment. These are generally not patient identifiers.
	{0x0018, 0x0010}: {vrLO, actionKeep},
	{0x0018, 0x0015}: {vrCS, actionKeep},
	{0x0018, 0x1000}: {vrLO, actionKeep},
	{0x0018, 0x1020}: {vrLO, actionKeep},
	{0x0018, 0x1030}: {vrLO, actionKeep},

	// Study/series/image attributes.
	{0x0020, 0x000D}: {vrUI, actionUID},
	{0x0020, 0x000E}: {vrUI, actionUID},
	{0x0020, 0x0010}: {vrSH, actionEmpty},
	{0x0020, 0x0011}: {vrIS, actionKeep},
	{0x0020, 0x0012}: {vrIS, actionKeep},
	{0x0020, 0x0013}: {vrIS, actionKeep},
	{0x0020, 0x0020}: {vrCS, actionKeep},
	{0x0020, 0x0032}: {vrDS, actionKeep},
	{0x0020, 0x0037}: {vrDS, actionKeep},
	{0x0020, 0x0052}: {vrUI, actionUID},
	{0x0020, 0x0200}: {vrUI, actionUID},
	{0x0020, 0x4000}: {vrLT, actionEmpty},
	{0x0020, 0x9161}: {vrUI, actionUID},
	{0x0020, 0x9164}: {vrUI, actionUID},

	// Image pixel and presentation information.
	{0x0028, 0x0002}: {vrUS, actionKeep},
	{0x0028, 0x0004}: {vrCS, actionKeep},
	{0x0028, 0x0010}: {vrUS, actionKeep},
	{0x0028, 0x0011}: {vrUS, actionKeep},
	{0x0028, 0x0100}: {vrUS, actionKeep},
	{0x0028, 0x0101}: {vrUS, actionKeep},
	{0x0028, 0x0102}: {vrUS, actionKeep},
	{0x0028, 0x0103}: {vrUS, actionKeep},
	{0x0028, 0x0301}: {vrCS, actionKeep},
	{0x0028, 0x0302}: {vrCS, actionKeep},
	{0x0028, 0x0303}: {vrCS, actionSetTemporalModified},
	{0x0028, 0x1050}: {vrDS, actionKeep},
	{0x0028, 0x1051}: {vrDS, actionKeep},
	{0x0028, 0x1052}: {vrDS, actionKeep},
	{0x0028, 0x1053}: {vrDS, actionKeep},
	{0x0028, 0x2110}: {vrCS, actionKeep},
	{0x0028, 0x2112}: {vrDS, actionKeep},
	{0x0028, 0x2114}: {vrCS, actionKeep},
	{0x0028, 0x4000}: {vrLT, actionEmpty},

	// Requested procedure and visit information.
	{0x0032, 0x1032}: {vrPN, actionEmpty},
	{0x0032, 0x1033}: {vrLO, actionEmpty},
	{0x0032, 0x1060}: {vrLO, actionEmpty},
	{0x0032, 0x1064}: {vrSQ, actionKeep},
	{0x0032, 0x4000}: {vrLT, actionEmpty},
	{0x0038, 0x0010}: {vrLO, actionEmpty},
	{0x0038, 0x0011}: {vrLO, actionEmpty},
	{0x0038, 0x0014}: {vrSQ, actionRemove},
	{0x0038, 0x0020}: {vrDA, actionDate},
	{0x0038, 0x0021}: {vrLO, actionEmpty},
	{0x0038, 0x0030}: {vrDA, actionDate},
	{0x0038, 0x0032}: {vrTM, actionEmpty},
	{0x0038, 0x0040}: {vrLT, actionEmpty},
	{0x0038, 0x0050}: {vrSQ, actionKeep},
	{0x0038, 0x0060}: {vrLO, actionEmpty},
	{0x0038, 0x0061}: {vrLO, actionEmpty},
	{0x0038, 0x0062}: {vrLO, actionEmpty},
	{0x0038, 0x0300}: {vrLO, actionEmpty},
	{0x0038, 0x0400}: {vrLO, actionEmpty},
	{0x0038, 0x0500}: {vrLO, actionEmpty},
	{0x0038, 0x4000}: {vrLT, actionEmpty},

	// Worklist, performed procedure, and request attributes.
	{0x0040, 0x0001}: {vrAE, actionEmpty},
	{0x0040, 0x0002}: {vrDA, actionDate},
	{0x0040, 0x0003}: {vrTM, actionEmpty},
	{0x0040, 0x0004}: {vrDA, actionDate},
	{0x0040, 0x0005}: {vrTM, actionEmpty},
	{0x0040, 0x0006}: {vrPN, actionEmpty},
	{0x0040, 0x0007}: {vrLO, actionEmpty},
	{0x0040, 0x0008}: {vrSQ, actionKeep},
	{0x0040, 0x0009}: {vrSH, actionEmpty},
	{0x0040, 0x0010}: {vrSH, actionEmpty},
	{0x0040, 0x0011}: {vrSH, actionEmpty},
	{0x0040, 0x0012}: {vrLO, actionKeep},
	{0x0040, 0x0100}: {vrSQ, actionKeep},
	{0x0040, 0x0241}: {vrAE, actionEmpty},
	{0x0040, 0x0242}: {vrSH, actionEmpty},
	{0x0040, 0x0243}: {vrSH, actionEmpty},
	{0x0040, 0x0244}: {vrDA, actionDate},
	{0x0040, 0x0245}: {vrTM, actionEmpty},
	{0x0040, 0x0250}: {vrDA, actionDate},
	{0x0040, 0x0251}: {vrTM, actionEmpty},
	{0x0040, 0x0253}: {vrSH, actionEmpty},
	{0x0040, 0x0254}: {vrLO, actionEmpty},
	{0x0040, 0x0255}: {vrLO, actionEmpty},
	{0x0040, 0x0275}: {vrSQ, actionKeep},
	{0x0040, 0x0280}: {vrLT, actionEmpty},
	{0x0040, 0x0555}: {vrSQ, actionKeep},
	{0x0040, 0x1001}: {vrSH, actionEmpty},
	{0x0040, 0x1002}: {vrLO, actionEmpty},
	{0x0040, 0x1003}: {vrLO, actionEmpty},
	{0x0040, 0x1004}: {vrSQ, actionKeep},
	{0x0040, 0x1005}: {vrLT, actionEmpty},
	{0x0040, 0x100A}: {vrLO, actionEmpty},
	{0x0040, 0x1010}: {vrPN, actionEmpty},
	{0x0040, 0x1011}: {vrSQ, actionRemove},
	{0x0040, 0x1400}: {vrLT, actionEmpty},
	{0x0040, 0x2001}: {vrLO, actionEmpty},
	{0x0040, 0x2004}: {vrDA, actionDate},
	{0x0040, 0x2005}: {vrTM, actionEmpty},
	{0x0040, 0x2006}: {vrSH, actionEmpty},
	{0x0040, 0x2007}: {vrSH, actionEmpty},
	{0x0040, 0x2008}: {vrPN, actionEmpty},
	{0x0040, 0x2009}: {vrLO, actionEmpty},
	{0x0040, 0x2010}: {vrSH, actionEmpty},
	{0x0040, 0x2011}: {vrLT, actionEmpty},
	{0x0040, 0x2016}: {vrLO, actionEmpty},
	{0x0040, 0x2017}: {vrLO, actionEmpty},
	{0x0040, 0x2400}: {vrLT, actionEmpty},

	// Structured report content and observer identities.
	{0x0040, 0xA073}: {vrSQ, actionKeep},
	{0x0040, 0xA075}: {vrPN, actionEmpty},
	{0x0040, 0xA078}: {vrSQ, actionKeep},
	{0x0040, 0xA07A}: {vrSQ, actionKeep},
	{0x0040, 0xA088}: {vrSQ, actionKeep},
	{0x0040, 0xA120}: {vrDT, actionDate},
	{0x0040, 0xA121}: {vrDA, actionDate},
	{0x0040, 0xA122}: {vrTM, actionKeep},
	{0x0040, 0xA123}: {vrPN, actionEmpty},
	{0x0040, 0xA124}: {vrUI, actionUID},
	{0x0040, 0xA160}: {vrUT, actionEmpty},
	{0x0040, 0xA171}: {vrUI, actionUID},
	{0x0040, 0xA730}: {vrSQ, actionKeep},

	// Presentation state and graphics. Annotation content must not survive.
	{0x0070, 0x0001}: {vrSQ, actionRemove},
	{0x0070, 0x0002}: {vrSQ, actionRemove},
	{0x0070, 0x0006}: {vrUT, actionEmpty},
	{0x0070, 0x0008}: {vrSQ, actionRemove},
	{0x0070, 0x0009}: {vrSQ, actionRemove},
	{0x0070, 0x0080}: {vrLO, actionEmpty},
	{0x0070, 0x0081}: {vrST, actionEmpty},
	{0x0070, 0x0082}: {vrDA, actionDate},
	{0x0070, 0x0083}: {vrTM, actionKeep},
	{0x0070, 0x0084}: {vrPN, actionEmpty},
	{0x0070, 0x0086}: {vrSQ, actionRemove},

	// Icon images often duplicate the full header, including private tags.
	{0x0088, 0x0200}: {vrSQ, actionKeep},
	{0x0088, 0x0130}: {vrSH, actionEmpty},
	{0x0088, 0x0140}: {vrUI, actionUID},

	// RT and segmentation identifiers and dates.
	{0x3006, 0x0002}: {vrLO, actionEmpty},
	{0x3006, 0x0004}: {vrLO, actionEmpty},
	{0x3006, 0x0006}: {vrLO, actionEmpty},
	{0x3006, 0x0008}: {vrDA, actionDate},
	{0x3006, 0x0009}: {vrTM, actionKeep},
	{0x3006, 0x0010}: {vrSQ, actionKeep},
	{0x3006, 0x0012}: {vrSQ, actionKeep},
	{0x3006, 0x0014}: {vrSQ, actionKeep},
	{0x3006, 0x0016}: {vrSQ, actionKeep},
	{0x3006, 0x0020}: {vrSQ, actionKeep},
	{0x3006, 0x0024}: {vrUI, actionUID},
	{0x3006, 0x0026}: {vrLO, actionKeep},
	{0x3006, 0x0028}: {vrST, actionKeep},
	{0x3006, 0x0039}: {vrSQ, actionKeep},
	{0x3006, 0x0040}: {vrSQ, actionKeep},
	{0x3006, 0x0080}: {vrSQ, actionKeep},
	{0x3006, 0x0082}: {vrIS, actionKeep},
	{0x3006, 0x0084}: {vrIS, actionKeep},
	{0x3006, 0x0085}: {vrSH, actionEmpty},
	{0x3006, 0x0086}: {vrSQ, actionKeep},
	{0x300A, 0x0002}: {vrLO, actionEmpty},
	{0x300A, 0x0003}: {vrLO, actionEmpty},
	{0x300A, 0x0004}: {vrST, actionEmpty},
	{0x300A, 0x0006}: {vrDA, actionDate},
	{0x300A, 0x0007}: {vrTM, actionKeep},
	{0x300A, 0x0010}: {vrSQ, actionKeep},
	{0x300A, 0x0070}: {vrSQ, actionKeep},
	{0x300A, 0x0080}: {vrSQ, actionKeep},

	// Enhanced multi-frame functional groups.
	{0x5200, 0x9229}: {vrSQ, actionKeep},
	{0x5200, 0x9230}: {vrSQ, actionKeep},

	// Pixel data and related containers.
	{0x7FE0, 0x0008}: {vrOF, actionKeep},
	{0x7FE0, 0x0009}: {vrOD, actionKeep},
	{0x7FE0, 0x0010}: {vrOW, actionKeep},
}

var safeUIDTags = map[Tag]bool{
	{0x0002, 0x0002}: true, // Media Storage SOP Class UID
	{0x0002, 0x0010}: true, // Transfer Syntax UID
	{0x0002, 0x0012}: true, // Implementation Class UID
	{0x0008, 0x0016}: true, // SOP Class UID
	{0x0008, 0x010C}: true, // Coding Scheme UID
	{0x0008, 0x010D}: true, // Context Group Extension Creator UID
	{0x0008, 0x0117}: true, // Coding Scheme UID
	{0x0008, 0x1150}: true, // Referenced SOP Class UID
	{0x0008, 0x9123}: true, // Creator Version UID
}

func vrForTag(tag Tag) vr {
	if entry, ok := tagDictionary[tag]; ok {
		return entry.vr
	}
	return vrUN
}

func actionForTag(tag Tag) dicomAction {
	if entry, ok := tagDictionary[tag]; ok {
		return entry.action
	}
	return actionKeep
}
