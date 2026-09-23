package objectgraph

// Wire format (version 1), all multi-byte integers are unsigned LEB128
// varints unless stated:
//
//	header  := magic(3) version(1) nodeCount(uvarint) rootIndex(uvarint)
//	record  := tag payload
//	tag     := 0=null | 1=bool | 2=int | 3=float | 4=string | 5=array | 6=object
//	bool    := byte(0|1)
//	int     := signed zigzag varint
//	float   := 8 bytes big-endian IEEE-754
//	string  := byteLen(uvarint) rawBytes
//	array   := len(uvarint) { targetNodeIndex(uvarint) }*
//	object  := fieldCount(uvarint) { key(string) targetNodeIndex(uvarint) }*
//
// Records appear in node-index order (0..nodeCount-1). Edges may point to any
// index, including a larger one: such forward references are staged pending
// and resolved once the target record has been parsed. The root index may be
// any node. The decoder rejects trailing bytes.
//
// Security properties of the format:
//   - every declared length is validated against limits and remaining input
//     before the backing slice/string is allocated;
//   - node indices are validated against nodeCount before use;
//   - there is no mechanism to name, import, reflect on or execute host code.
const (
	magic0   = 'O'
	magic1   = 'G'
	magic2   = '1'
	version1 = 1

	tagNull   = 0
	tagBool   = 1
	tagInt    = 2
	tagFloat  = 3
	tagString = 4
	tagArray  = 5
	tagObject = 6
)
