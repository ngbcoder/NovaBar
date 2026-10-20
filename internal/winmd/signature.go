package winmd

import (
	"fmt"
	"strings"
)

// Signature is a decoded method or field signature.
type Signature struct {
	HasThis bool
	Return  Type
	Params  []Type
}

// TypeKind classifies a signature type.
type TypeKind int

const (
	// KindVoid is System.Void.
	KindVoid TypeKind = iota
	// KindPrimitive is a built-in numeric or boolean type.
	KindPrimitive
	// KindString is System.String.
	KindString
	// KindObject is System.Object.
	KindObject
	// KindClass is a reference type: an interface or a class.
	KindClass
	// KindValueType is a value type: a struct or an enum.
	KindValueType
	// KindGenericInst is a constructed generic type.
	KindGenericInst
	// KindArray is a single dimensional array.
	KindArray
	// KindUnknown is a type this reader does not model.
	KindUnknown
)

// Type is a decoded signature type.
type Type struct {
	Kind    TypeKind
	Elem    uint8
	Ref     TypeRef
	Generic []Type
	Rank    int
	Inner   *Type
}

// primitiveNames maps ECMA-335 element types to their names.
var primitiveNames = map[uint8]string{
	0x01: "void", 0x02: "bool", 0x03: "char", 0x04: "i1", 0x05: "u1",
	0x06: "i2", 0x07: "u2", 0x08: "i4", 0x09: "u4", 0x0A: "i8", 0x0B: "u8",
	0x0C: "r4", 0x0D: "r8", 0x0E: "string", 0x16: "typedref", 0x17: "i",
	0x18: "u", 0x1A: "object", 0x41: "enum",
}

// String renders the type for diagnostics.
func (t Type) String() string {
	switch t.Kind {
	case KindVoid:
		return "void"
	case KindPrimitive:
		if name, ok := primitiveNames[t.Elem]; ok {
			return name
		}
		return fmt.Sprintf("primitive(%#x)", t.Elem)
	case KindString:
		return "string"
	case KindObject:
		return "object"
	case KindClass, KindValueType:
		return t.Ref.String()
	case KindGenericInst:
		var b strings.Builder
		if t.Inner != nil {
			b.WriteString(t.Inner.String())
		}
		b.WriteString("<")
		for i, a := range t.Generic {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(a.String())
		}
		b.WriteString(">")
		return b.String()
	case KindArray:
		inner := ""
		if t.Inner != nil {
			inner = t.Inner.String()
		}
		return inner + "[]"
	}
	return "unknown"
}

// String renders a type reference.
func (r TypeRef) String() string {
	switch r.Table {
	case tblTypeDef:
		return fmt.Sprintf("typedef#%d", r.Index)
	case tblTypeRef:
		return fmt.Sprintf("typeref#%d", r.Index)
	case tblTypeSpec:
		return fmt.Sprintf("typespec#%d", r.Index)
	}
	return fmt.Sprintf("type#%d", r.Index)
}

// parseMethodSignature decodes a MethodDefSig blob.
func parseMethodSignature(blob []byte) Signature {
	s := Signature{}
	p := &sigParser{data: blob}

	callConv := p.u1()
	s.HasThis = callConv&0x20 != 0

	count := p.compressedUint()
	ret := p.typeRef()
	s.Return = ret
	for i := uint32(0); i < count; i++ {
		s.Params = append(s.Params, p.typeRef())
	}
	return s
}

// parseFieldSignature decodes a FieldSig blob.
func parseFieldSignature(blob []byte) Signature {
	s := Signature{}
	p := &sigParser{data: blob}
	if len(blob) == 0 || blob[0] != 0x06 {
		return s
	}
	p.pos = 1
	s.Return = p.typeRef()
	return s
}

// parseTypeSpecSignature decodes a TypeSpec blob.
func parseTypeSpecSignature(blob []byte) Signature {
	s := Signature{}
	p := &sigParser{data: blob}
	s.Return = p.typeRef()
	return s
}

// sigParser walks a signature blob.
type sigParser struct {
	data []byte
	pos  int
}

// u1 reads one byte.
func (p *sigParser) u1() uint8 {
	if p.pos >= len(p.data) {
		return 0
	}
	v := p.data[p.pos]
	p.pos++
	return v
}

// compressedUint reads a compressed unsigned integer.
func (p *sigParser) compressedUint() uint32 {
	v, n := readCompressedUint(p.data[p.pos:])
	p.pos += n
	return v
}

// typeRef reads one type from the signature.
//
// The element type codes are the ECMA-335 ones. They are not in numeric order and
// several of them sit next to each other with different meanings, so they are spelled
// out rather than ranged over.
func (p *sigParser) typeRef() Type {
	e := p.u1()
	switch e {
	case 0x01:
		return Type{Kind: KindVoid, Elem: e}
	case 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D,
		0x16, 0x17, 0x18, 0x1A, 0x41:
		return Type{Kind: KindPrimitive, Elem: e}
	case 0x0E:
		return Type{Kind: KindString, Elem: e}
	case 0x11, 0x12:
		ref := p.typeDefOrRef()
		kind := KindValueType
		if e == 0x12 {
			kind = KindClass
		}
		return Type{Kind: kind, Elem: e, Ref: ref}
	case 0x1B:
		inner := p.typeRef()
		return Type{Kind: KindArray, Elem: e, Inner: &inner, Rank: 1}
	case 0x15:
		base := p.typeRef()
		count := p.compressedUint()
		args := make([]Type, 0, count)
		for i := uint32(0); i < count; i++ {
			args = append(args, p.typeRef())
		}
		return Type{Kind: KindGenericInst, Elem: e, Generic: args, Inner: &base}
	case 0x1D, 0x1E: // custom modifiers
		_ = p.typeDefOrRef()
		return p.typeRef()
	case 0x22: // PINNED
		return p.typeRef()
	}
	return Type{Kind: KindUnknown, Elem: e}
}

// typeDefOrRef reads a TypeDefOrRef coded index from a signature blob.
func (p *sigParser) typeDefOrRef() TypeRef {
	compressed := p.compressedUint()
	tag := int(compressed & 0x03)
	idx := compressed >> 2
	tbl := codedIndexes[ciTypeDefOrRef].tables[tag]
	return TypeRef{Table: tbl, Index: idx}
}
