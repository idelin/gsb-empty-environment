package ionfilter

import "bytes"

// Binary Ion type codes from the Ion specification. Only the ones needed for
// structural validation are named.
const (
	binaryNull       = 0
	binaryString     = 8
	binarySymbol     = 7
	binaryList       = 11
	binarySexp       = 12
	binaryStruct     = 13
	binaryAnnotation = 14
)

type scanContext struct {
	allowedSet map[string]struct{}
	trust      boundaryList
	table      *symbolTable
}

func (c *scanContext) allowed(text string) bool {
	_, ok := c.allowedSet[text]
	return ok
}

type binaryField struct {
	nameSID int
	value   *binaryNode
}

type binaryNode struct {
	start       int
	end         int
	typeCode    int
	annotations []int
	symbolID    int
	isSymbol    bool
	stringText  string
	fields      []binaryField
	children    []*binaryNode
}

func (c *scanContext) scanBinary(data []byte) error {
	offset := 0
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		offset = 3
	}
	if len(data)-offset < 4 || !bytes.Equal(data[offset:offset+4], binaryIVM) {
		return malformed(offset, "binary Ion stream must begin with the IVM")
	}
	offset += 4

	for offset < len(data) {
		if len(data)-offset >= 4 && bytes.Equal(data[offset:offset+4], binaryIVM) {
			return policyError(KindIVM, offset, "binary IVM is only permitted at the start of the stream")
		}
		node, err := readBinaryValue(data, offset)
		if err != nil {
			return err
		}
		if err := c.checkBinaryNode(node); err != nil {
			return err
		}
		offset = node.end
	}
	return nil
}

func readBinaryValue(data []byte, offset int) (*binaryNode, error) {
	start := offset
	if offset >= len(data) {
		return nil, malformed(offset, "unexpected end of stream")
	}
	header := data[offset]
	offset++
	typeCode := int(header >> 4)
	lowNibble := int(header & 0x0F)

	if typeCode == binaryNull {
		// NOP padding (0x00) and typed nulls (0x01..0x0F) carry no body.
		return &binaryNode{start: start, end: offset, typeCode: typeCode}, nil
	}

	length := lowNibble
	if lowNibble == 0x0E {
		var err error
		length, offset, err = readVarUInt(data, offset)
		if err != nil {
			return nil, err
		}
	} else if lowNibble == 0x0F {
		return nil, malformed(start, "reserved length nibble 0x0F")
	}

	bodyStart := offset
	bodyEnd := offset + length
	if bodyEnd > len(data) {
		return nil, malformed(start, "value body of %d bytes exceeds stream", length)
	}

	node := &binaryNode{start: start, end: bodyEnd, typeCode: typeCode}

	if typeCode == binaryAnnotation {
		annotLen, afterLen, err := readVarUInt(data, bodyStart)
		if err != nil {
			return nil, err
		}
		annotEnd := afterLen + annotLen
		if annotEnd > bodyEnd {
			return nil, malformed(start, "annotation length exceeds wrapper body")
		}
		sids, err := readVarUIntList(data, afterLen, annotEnd)
		if err != nil {
			return nil, err
		}
		inner, err := readBinaryValue(data, annotEnd)
		if err != nil {
			return nil, err
		}
		if inner.end != bodyEnd {
			return nil, malformed(start, "annotation wrapper must contain exactly one value")
		}
		node.annotations = sids
		node.typeCode = inner.typeCode
		node.isSymbol = inner.isSymbol
		node.symbolID = inner.symbolID
		node.stringText = inner.stringText
		node.fields = inner.fields
		node.children = inner.children
		return node, nil
	}

	switch typeCode {
	case binarySymbol:
		sid, err := readUInt(data, bodyStart, length)
		if err != nil {
			return nil, err
		}
		node.isSymbol = true
		node.symbolID = sid
	case binaryString:
		node.stringText = string(data[bodyStart:bodyEnd])
	case binaryStruct:
		pos := bodyStart
		for pos < bodyEnd {
			nameSID, afterName, err := readVarUInt(data, pos)
			if err != nil {
				return nil, err
			}
			child, err := readBinaryValue(data, afterName)
			if err != nil {
				return nil, err
			}
			if child.end > bodyEnd {
				return nil, malformed(start, "struct field exceeds container body")
			}
			node.fields = append(node.fields, binaryField{nameSID: nameSID, value: child})
			pos = child.end
		}
	case binaryList, binarySexp:
		pos := bodyStart
		for pos < bodyEnd {
			child, err := readBinaryValue(data, pos)
			if err != nil {
				return nil, err
			}
			if child.end > bodyEnd {
				return nil, malformed(start, "container element exceeds container body")
			}
			node.children = append(node.children, child)
			pos = child.end
		}
	}
	return node, nil
}

func readVarUInt(data []byte, offset int) (int, int, error) {
	start := offset
	value := 0
	for {
		if offset >= len(data) {
			return 0, offset, malformed(start, "unterminated VarUInt")
		}
		b := data[offset]
		offset++
		value = (value << 7) | int(b&0x7F)
		if b&0x80 != 0 {
			return value, offset, nil
		}
		if offset-start >= 9 {
			return 0, offset, malformed(start, "VarUInt encoding is too long")
		}
	}
}

func readVarUIntList(data []byte, start, end int) ([]int, error) {
	var sids []int
	pos := start
	for pos < end {
		sid, next, err := readVarUInt(data, pos)
		if err != nil {
			return nil, err
		}
		sids = append(sids, sid)
		pos = next
	}
	return sids, nil
}

func readUInt(data []byte, offset, length int) (int, error) {
	if length == 0 {
		return 0, nil
	}
	if length > 8 || offset+length > len(data) {
		return 0, malformed(offset, "invalid UInt of %d bytes", length)
	}
	value := 0
	for i := 0; i < length; i++ {
		value = value<<8 | int(data[offset+i])
	}
	return value, nil
}

func (c *scanContext) checkBinaryNode(node *binaryNode) error {
	trusted := c.trust.trustedAt(node.start)
	directive := false
	for _, sid := range node.annotations {
		text, err := c.table.resolve(sid)
		if err != nil {
			return policyError(KindAnnotation, node.start, "annotation SID %d cannot be resolved", sid)
		}
		if isDirectiveAnnotation(text) {
			if !trusted {
				return policyError(KindSymbolTable, node.start,
					"symbol table directive %q is forbidden in user data", text)
			}
			directive = true
			continue
		}
		if !c.allowed(text) {
			return policyError(KindAnnotation, node.start, "annotation %q is not in the allow list", text)
		}
	}
	if directive {
		if err := c.applyBinaryDirective(node); err != nil {
			return err
		}
	}
	for _, field := range node.fields {
		if _, err := c.table.resolve(field.nameSID); err != nil {
			return malformed(node.start, "struct field name SID %d cannot be resolved", field.nameSID)
		}
		if err := c.checkBinaryNode(field.value); err != nil {
			return err
		}
	}
	for _, child := range node.children {
		if err := c.checkBinaryNode(child); err != nil {
			return err
		}
	}
	return nil
}

// applyBinaryDirective consumes a trusted $ion_symbol_table annotated struct.
// Shared symbol table imports cannot be resolved without a catalog, so an
// imported table resets to the system table and any SID only provided by that
// import later fails closed during resolution.
func (c *scanContext) applyBinaryDirective(node *binaryNode) error {
	for _, sid := range node.annotations {
		text, err := c.table.resolve(sid)
		if err != nil {
			return malformed(node.start, "directive annotation SID %d cannot be resolved", sid)
		}
		if text == annSharedSymbolTable {
			// Shared table declarations are self contained; nothing to bind.
			continue
		}
		if text != annSymbolTable {
			continue
		}
		if node.typeCode != binaryStruct {
			return malformed(node.start, "local symbol table directive must annotate a struct")
		}
		var symbolField *binaryNode
		for _, field := range node.fields {
			name, err := c.table.resolve(field.nameSID)
			if err != nil {
				return malformed(node.start, "directive field name cannot be resolved")
			}
			switch name {
			case "imports":
				if field.value.isSymbol && field.value.symbolID == 3 {
					// Explicit substitution of the local symbol table.
					c.table.reset()
				}
			case "symbols":
				symbolField = field.value
			}
		}
		if symbolField != nil {
			names := make([]string, 0, len(symbolField.children))
			for _, child := range symbolField.children {
				if child.typeCode == binaryNull {
					names = append(names, "")
					continue
				}
				if !child.isSymbol && child.typeCode != binaryString {
					return malformed(node.start, "symbols list must contain symbol values")
				}
				if child.typeCode == binaryString {
					names = append(names, child.stringText)
					continue
				}
				text, err := c.table.resolve(child.symbolID)
				if err != nil {
					return malformed(node.start, "symbol in symbols list cannot be resolved")
				}
				names = append(names, text)
			}
			c.table.appendLocal(names)
		}
	}
	return nil
}
