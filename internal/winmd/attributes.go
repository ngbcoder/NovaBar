package winmd

import (
	"encoding/binary"
)

// CustomAttribute is one attribute applied to a metadata entity.
type CustomAttribute struct {
	// TypeName is the attribute's type name, without the namespace.
	TypeName string
	// Value is the serialized attribute blob.
	Value []byte
}

// CustomAttributes returns the attributes applied to a type definition.
//
// WinRT does not put interface identifiers in the GUID heap. Each interface carries a
// GuidAttribute whose value is the interface's IID, so the attribute blob is where
// the identifier actually lives.
func (f *File) CustomAttributes(def TypeDef) []CustomAttribute {
	t := f.tables

	parentWidth := t.codedSize(ciHasCustomAttribute)
	typeWidth := t.codedSize(ciCustomAttributeType)

	var out []CustomAttribute
	for i := 1; i <= int(t.rows[tblCustomAttribute]); i++ {
		row := t.row(tblCustomAttribute, i)
		if row == nil {
			continue
		}
		c := &rowCursor{data: row}
		parentRaw := c.index(parentWidth)
		attrType := c.index(typeWidth)
		value := f.BlobAt(c.index(t.heapIndexSize(colBlob)))

		parentTable := codedIndexes[ciHasCustomAttribute].tables[int(parentRaw&((1<<5)-1))]
		parentIndex := parentRaw >> 5
		if parentTable != tblTypeDef || int(parentIndex) != def.Index {
			continue
		}

		name := f.attributeTypeName(attrType)
		if name == "" {
			continue
		}
		out = append(out, CustomAttribute{TypeName: name, Value: value})
	}
	return out
}

// attributeTypeName resolves the type of a custom attribute.
//
// The attribute type is either a MethodDef on the attribute's own type or a MemberRef
// naming a constructor, and in both cases the name is what identifies it.
func (f *File) attributeTypeName(attrType uint32) string {
	t := f.tables

	tag := int(attrType & 0x07)
	index := attrType >> 3

	switch tag {
	case 2: // MethodDef: the attribute type is the one declaring that method.
		for _, def := range f.TypeDefs() {
			if int(index) >= def.MethodFrom && int(index) < def.MethodTo {
				return def.Name
			}
		}
		return ""
	case 3: // MemberRef
		row := t.row(tblMemberRef, int(index))
		if row == nil {
			return ""
		}
		c := &rowCursor{data: row}
		parentRaw := c.index(t.codedSize(ciMemberRefParent))

		parentTag := int(parentRaw & 0x07)
		parentIndex := parentRaw >> 3
		switch codedIndexes[ciMemberRefParent].tables[parentTag] {
		case tblTypeDef:
			defs := f.TypeDefs()
			if int(parentIndex) >= 1 && int(parentIndex) <= len(defs) {
				return defs[parentIndex-1].Name
			}
		case tblTypeRef:
			return f.ResolveTypeName(TypeRef{Table: tblTypeRef, Index: parentIndex})
		}
		return ""
	}
	return ""
}

// Constant is a compile-time constant attached to a field or parameter.
type Constant struct {
	// ParentTable is the table the constant belongs to.
	ParentTable int
	// ParentIndex is the row in that table.
	ParentIndex uint32
	// Type is the constant's blob type.
	Type uint8
	// Value is the constant's raw value.
	Value []byte
}

// Constants returns the constants attached to a type definition's fields.
//
// Enum members are constants on fields, so this is how an enum's numeric values are
// recovered. Reading them from metadata rather than from documentation keeps the
// values in step with the operating system that will be asked to interpret them.
func (f *File) Constants(def TypeDef) []Constant {
	t := f.tables

	parentWidth := t.codedSize(ciHasConstant)
	fields := f.Fields(def)
	if len(fields) == 0 {
		return nil
	}

	var out []Constant
	for i := 1; i <= int(t.rows[tblConstant]); i++ {
		row := t.row(tblConstant, i)
		if row == nil {
			continue
		}
		c := &rowCursor{data: row}
		typ := c.u1()
		_ = c.u1() // padding
		parentRaw := c.index(parentWidth)
		value := f.BlobAt(c.index(t.heapIndexSize(colBlob)))

		parentTable := codedIndexes[ciHasConstant].tables[int(parentRaw&0x03)]
		parentIndex := parentRaw >> 2
		if parentTable != tblField {
			continue
		}
		for _, fl := range fields {
			if fl.Row == int(parentIndex) {
				out = append(out, Constant{
					ParentTable: parentTable,
					ParentIndex: parentIndex,
					Type:        typ,
					Value:       value,
				})
			}
		}
	}
	return out
}

// ConstantValue returns the integer value of a constant.
func (c Constant) ConstantValue() (int64, bool) {
	switch c.Type {
	case 0x03: // ELEMENT_TYPE_BOOLEAN
		if len(c.Value) == 0 {
			return 0, false
		}
		return int64(c.Value[0]), true
	case 0x04, 0x05: // I1, U1
		if len(c.Value) == 0 {
			return 0, false
		}
		return int64(c.Value[0]), true
	case 0x06, 0x07, 0x08, 0x09: // I2, U2, I4, U4
		if len(c.Value) < 4 {
			return 0, false
		}
		return int64(binary.LittleEndian.Uint32(c.Value)), true
	case 0x0A, 0x0B: // I8, U8
		if len(c.Value) < 8 {
			return 0, false
		}
		return int64(binary.LittleEndian.Uint64(c.Value)), true
	}
	return 0, false
}

// InterfaceGUID returns the IID of an interface, by full name.
func (f *File) InterfaceGUID(fullName string) (GUID, bool) {
	def, ok := f.FindTypeDef(fullName)
	if !ok {
		return GUID{}, false
	}
	return f.TypeGUID(def)
}

// TypeGUID returns the interface identifier of a type.
//
// The identifier is read from the GuidAttribute applied to the type, which is how
// WinRT metadata records it. A type without the attribute is not an interface with
// a published IID.
func (f *File) TypeGUID(def TypeDef) (GUID, bool) {
	for _, attr := range f.CustomAttributes(def) {
		if simpleName(attr.TypeName) != "GuidAttribute" {
			continue
		}
		return decodeGuidAttribute(attr.Value)
	}
	return GUID{}, false
}

// simpleName strips a namespace from a type name.
func simpleName(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			return name[i+1:]
		}
	}
	return name
}

// decodeGuidAttribute decodes the value blob of a GuidAttribute.
//
// The blob is a prolog followed by the fixed argument: a Guid, which serializes as
// its four components in little endian order.
func decodeGuidAttribute(value []byte) (GUID, bool) {
	if len(value) < 2+11 {
		return GUID{}, false
	}
	// Prolog 0x0001 marks a fixed argument list.
	if binary.LittleEndian.Uint16(value) != 1 {
		return GUID{}, false
	}

	arg := value[2:]
	g := GUID{
		Data1: binary.LittleEndian.Uint32(arg),
		Data2: binary.LittleEndian.Uint16(arg[4:]),
		Data3: binary.LittleEndian.Uint16(arg[6:]),
	}
	copy(g.Data4[:], arg[8:16])
	return g, true
}
