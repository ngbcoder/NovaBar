// Package winmd reads Windows Runtime metadata (.winmd) files.
//
// It exists because a WinRT interface's IID, its vtable slot order and its struct
// layouts are all defined in metadata, and guessing them is unsafe: a wrong IID fails
// as E_NOINTERFACE, which reads plausibly, and a wrong vtable slot terminates the
// process instead of returning an error. The metadata that ships with the running
// operating system is the ground truth for all three, so it is parsed directly.
//
// The reader is pure Go and reads the file format, it does not load the metadata
// through COM. That keeps it testable, and keeps the tree's no-cgo constraint intact.
//
// Only the subset needed by the media bridge is exposed. The parser is deliberately
// small: it models the PE container, the metadata heaps, and the tables that describe
// types, methods and fields.
package winmd

import (
	"encoding/binary"
	"fmt"
	"os"
	"sort"
	"strings"
)

// File is a parsed .winmd metadata file.
type File struct {
	streams map[string][]byte
	tables  *tableStream
}

// GUID is a Windows interface identifier.
type GUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

// String formats the GUID in the canonical braced form.
func (g GUID) String() string {
	return fmt.Sprintf("{%08x-%04x-%04x-%02x%02x-%02x%02x%02x%02x%02x%02x}",
		g.Data1, g.Data2, g.Data3,
		g.Data4[0], g.Data4[1], g.Data4[2], g.Data4[3],
		g.Data4[4], g.Data4[5], g.Data4[6], g.Data4[7])
}

// IsZero reports whether the GUID is all zeroes.
func (g GUID) IsZero() bool {
	return g == GUID{}
}

// Load reads a .winmd file from disk.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse reads a .winmd image from memory.
func Parse(data []byte) (*File, error) {
	pe, err := parsePE(data)
	if err != nil {
		return nil, err
	}

	f := &File{streams: map[string][]byte{}}
	for _, s := range pe.metadataStreams {
		f.streams[s.name] = pe.metadata[s.offset : s.offset+s.size]
	}

	raw, ok := f.streams["#~"]
	if !ok {
		raw, ok = f.streams["#-"]
	}
	if !ok {
		return nil, fmt.Errorf("winmd: no metadata tables stream")
	}

	tables, err := parseTables(raw)
	if err != nil {
		return nil, err
	}
	f.tables = tables
	return f, nil
}

// --- PE container -------------------------------------------------------------

type streamRange struct {
	name   string
	offset int
	size   int
}

type peImage struct {
	metadata        []byte
	metadataStreams []streamRange
}

// parsePE walks the PE headers to find the CLI header and its metadata streams.
func parsePE(data []byte) (*peImage, error) {
	if len(data) < 0x40 || string(data[:2]) != "MZ" {
		return nil, fmt.Errorf("winmd: not a PE image")
	}

	peOffset := int(binary.LittleEndian.Uint32(data[0x3C:0x40]))
	if peOffset <= 0 || peOffset+24 > len(data) {
		return nil, fmt.Errorf("winmd: bad PE header offset")
	}
	if string(data[peOffset:peOffset+4]) != "PE\x00\x00" {
		return nil, fmt.Errorf("winmd: not a PE image")
	}

	coff := peOffset + 4
	sections := int(binary.LittleEndian.Uint16(data[coff+2:]))
	sizeOfOptional := int(binary.LittleEndian.Uint16(data[coff+16:]))

	optional := coff + 20
	magic := binary.LittleEndian.Uint16(data[optional:])

	// The CLI header is data directory entry 14. Its position inside the optional
	// header differs between PE32 and PE32+.
	var dirOffset int
	switch magic {
	case 0x10b: // PE32
		dirOffset = optional + 96
	case 0x20b: // PE32+
		dirOffset = optional + 112
	default:
		return nil, fmt.Errorf("winmd: unknown optional header magic %#x", magic)
	}
	if dirOffset+15*8 > len(data) {
		return nil, fmt.Errorf("winmd: truncated optional header")
	}

	cliRVA := binary.LittleEndian.Uint32(data[dirOffset+14*8:])
	if cliRVA == 0 {
		return nil, fmt.Errorf("winmd: no CLI header")
	}

	// Section table, needed to map RVAs to file offsets.
	sectionTable := optional + sizeOfOptional
	type section struct {
		virtualSize uint32
		virtualAddr uint32
		rawSize     uint32
		rawPointer  uint32
	}
	sects := make([]section, 0, sections)
	for i := 0; i < sections; i++ {
		base := sectionTable + i*40
		if base+40 > len(data) {
			return nil, fmt.Errorf("winmd: truncated section table")
		}
		sects = append(sects, section{
			virtualSize: binary.LittleEndian.Uint32(data[base+8:]),
			virtualAddr: binary.LittleEndian.Uint32(data[base+12:]),
			rawSize:     binary.LittleEndian.Uint32(data[base+16:]),
			rawPointer:  binary.LittleEndian.Uint32(data[base+20:]),
		})
	}

	rvaToOffset := func(rva uint32) (int, bool) {
		for _, s := range sects {
			size := s.virtualSize
			if s.rawSize > size {
				size = s.rawSize
			}
			if rva >= s.virtualAddr && rva < s.virtualAddr+size {
				return int(s.rawPointer + (rva - s.virtualAddr)), true
			}
		}
		return 0, false
	}

	cliOffset, ok := rvaToOffset(cliRVA)
	if !ok || cliOffset+16 > len(data) {
		return nil, fmt.Errorf("winmd: bad CLI header RVA")
	}

	metaRVA := binary.LittleEndian.Uint32(data[cliOffset+8:])
	metaSize := binary.LittleEndian.Uint32(data[cliOffset+12:])
	metaOffset, ok := rvaToOffset(metaRVA)
	if !ok || metaOffset+int(metaSize) > len(data) {
		return nil, fmt.Errorf("winmd: bad metadata RVA")
	}

	streams, err := parseMetadataRoot(data[metaOffset : metaOffset+int(metaSize)])
	if err != nil {
		return nil, err
	}
	return &peImage{metadata: data[metaOffset : metaOffset+int(metaSize)], metadataStreams: streams}, nil
}

// parseMetadataRoot reads the metadata root and its stream headers.
func parseMetadataRoot(data []byte) ([]streamRange, error) {
	if len(data) < 20 || binary.LittleEndian.Uint32(data) != 0x424A5342 {
		return nil, fmt.Errorf("winmd: bad metadata signature")
	}

	versionLen := int(binary.LittleEndian.Uint32(data[12:]))
	pos := 16 + versionLen
	// The version string is padded to a four byte boundary.
	if pad := pos % 4; pad != 0 {
		pos += 4 - pad
	}
	if pos+4 > len(data) {
		return nil, fmt.Errorf("winmd: truncated metadata root")
	}

	flags := binary.LittleEndian.Uint16(data[pos:])
	numStreams := int(binary.LittleEndian.Uint16(data[pos+2:]))
	pos += 4

	streams := make([]streamRange, 0, numStreams)
	for i := 0; i < numStreams; i++ {
		if pos+8 > len(data) {
			return nil, fmt.Errorf("winmd: truncated stream header")
		}
		offset := int(binary.LittleEndian.Uint32(data[pos:]))
		size := int(binary.LittleEndian.Uint32(data[pos+4:]))
		pos += 8

		end := pos
		for end < len(data) && data[end] != 0 {
			end++
		}
		name := string(data[pos:end])
		pos = end + 1
		if pad := pos % 4; pad != 0 {
			pos += 4 - pad
		}

		if offset+size > len(data) {
			return nil, fmt.Errorf("winmd: stream %q out of range", name)
		}
		streams = append(streams, streamRange{name: name, offset: offset, size: size})
	}

	_ = flags
	return streams, nil
}

// --- heaps --------------------------------------------------------------------

// String reads a null terminated UTF-8 string from the #Strings heap.
func (f *File) String(index uint32) string {
	heap := f.streams["#Strings"]
	if int(index) >= len(heap) {
		return ""
	}
	end := index
	for end < uint32(len(heap)) && heap[end] != 0 {
		end++
	}
	return string(heap[index:end])
}

// GUIDCount returns the number of GUIDs in the #GUID heap.
func (f *File) GUIDCount() uint32 {
	return uint32(len(f.streams["#GUID"]) / 16)
}

// GUIDAt reads a 1-based indexed GUID from the #GUID heap.
func (f *File) GUIDAt(index uint32) (GUID, bool) {
	heap := f.streams["#GUID"]
	off := (index - 1) * 16
	if index == 0 || int(off)+16 > len(heap) {
		return GUID{}, false
	}
	return GUID{
		Data1: binary.LittleEndian.Uint32(heap[off:]),
		Data2: binary.LittleEndian.Uint16(heap[off+4:]),
		Data3: binary.LittleEndian.Uint16(heap[off+6:]),
		Data4: [8]byte{
			heap[off+8], heap[off+9], heap[off+10], heap[off+11],
			heap[off+12], heap[off+13], heap[off+14], heap[off+15],
		},
	}, true
}

// BlobAt reads a 1-based indexed blob from the #Blob heap.
func (f *File) BlobAt(index uint32) []byte {
	heap := f.streams["#Blob"]
	if index == 0 || int(index) > len(heap) {
		return nil
	}
	off := int(index)
	length, n := readCompressedUint(heap[off:])
	if n == 0 {
		return nil
	}
	off += n
	if off+int(length) > len(heap) {
		return nil
	}
	return heap[off : off+int(length)]
}

// readCompressedUint decodes an ECMA-335 compressed unsigned integer.
func readCompressedUint(data []byte) (uint32, int) {
	if len(data) == 0 {
		return 0, 0
	}
	b := data[0]
	switch {
	case b&0x80 == 0:
		return uint32(b), 1
	case b&0xC0 == 0x80:
		if len(data) < 2 {
			return 0, 0
		}
		return uint32(b&0x3F)<<8 | uint32(data[1]), 2
	case b&0xE0 == 0xC0:
		if len(data) < 4 {
			return 0, 0
		}
		return uint32(b&0x1F)<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3]), 4
	}
	return 0, 0
}

// --- table stream -------------------------------------------------------------

type tableStream struct {
	data      []byte
	heapSizes byte
	valid     uint64
	sorted    uint64
	rows      [64]uint32
	offsets   [64]uint32
	sizes     [64]uint32
}

// parseTables reads the tables stream header and locates every table's rows.
func parseTables(data []byte) (*tableStream, error) {
	if len(data) < 24 || binary.LittleEndian.Uint32(data) != 0 {
		return nil, fmt.Errorf("winmd: bad tables stream")
	}

	t := &tableStream{data: data}
	t.heapSizes = data[6]
	t.valid = binary.LittleEndian.Uint64(data[8:])
	t.sorted = binary.LittleEndian.Uint64(data[16:])

	pos := 24
	for i := 0; i < 64; i++ {
		if t.valid&(1<<uint(i)) == 0 {
			continue
		}
		if pos+4 > len(data) {
			return nil, fmt.Errorf("winmd: truncated row counts")
		}
		t.rows[i] = binary.LittleEndian.Uint32(data[pos:])
		pos += 4
	}

	// Walk the tables in order, computing each one's row size so the next one can be
	// located. A table that is absent contributes nothing but its schema still
	// participates in the index-width rules.
	for i := 0; i < 64; i++ {
		if t.valid&(1<<uint(i)) == 0 {
			continue
		}
		if i >= len(tables) {
			return nil, fmt.Errorf("winmd: unknown table %#x", i)
		}
		size := t.rowSize(i)
		t.sizes[i] = uint32(size)
		t.offsets[i] = uint32(pos)
		pos += size * int(t.rows[i])
		if pos > len(data) {
			return nil, fmt.Errorf("winmd: table %#x overruns the stream", i)
		}
	}

	return t, nil
}

// indexSize returns the width of a plain table index column.
func (t *tableStream) indexSize(table int) int {
	if t.rows[table] < 0x10000 {
		return 2
	}
	return 4
}

// codedSize returns the width of a coded index column.
func (t *tableStream) codedSize(ci int) int {
	c := codedIndexes[ci]
	max := uint32(0)
	for _, tbl := range c.tables {
		if tbl == 0xFF {
			continue
		}
		if t.rows[tbl] > max {
			max = t.rows[tbl]
		}
	}
	if max < uint32(1)<<(16-c.tagBits) {
		return 2
	}
	return 4
}

// heapIndexSize returns the width of a heap index column.
func (t *tableStream) heapIndexSize(kind colKind) int {
	switch kind {
	case colString:
		if t.heapSizes&0x01 != 0 {
			return 4
		}
	case colGUID:
		if t.heapSizes&0x02 != 0 {
			return 4
		}
	case colBlob:
		if t.heapSizes&0x04 != 0 {
			return 4
		}
	}
	return 2
}

// rowSize computes the byte size of one row of a table.
func (t *tableStream) rowSize(table int) int {
	size := 0
	for _, c := range tables[table] {
		switch c.kind {
		case colU1:
			size++
		case colU2:
			size += 2
		case colU4:
			size += 4
		case colString, colGUID, colBlob:
			size += t.heapIndexSize(c.kind)
		case colTable:
			size += t.indexSize(c.table)
		case colCoded:
			size += t.codedSize(c.coded)
		}
	}
	return size
}

// rowCursor walks the columns of one row.
type rowCursor struct {
	data []byte
	pos  int
}

// u1 reads a one byte value.
func (r *rowCursor) u1() uint8 {
	v := r.data[r.pos]
	r.pos++
	return v
}

// u2 reads a little endian two byte value.
func (r *rowCursor) u2() uint16 {
	v := binary.LittleEndian.Uint16(r.data[r.pos:])
	r.pos += 2
	return v
}

// u4 reads a little endian four byte value.
func (r *rowCursor) u4() uint32 {
	v := binary.LittleEndian.Uint32(r.data[r.pos:])
	r.pos += 4
	return v
}

// index reads a heap or table index of the given width.
func (r *rowCursor) index(width int) uint32 {
	if width == 2 {
		return uint32(r.u2())
	}
	return r.u4()
}

// row returns the raw bytes of the given 1-based row of a table.
func (t *tableStream) row(table int, row int) []byte {
	if row < 1 || row > int(t.rows[table]) {
		return nil
	}
	start := int(t.offsets[table]) + (row-1)*int(t.sizes[table])
	return t.data[start : start+int(t.sizes[table])]
}

// --- typed table access -------------------------------------------------------

// TypeDef describes a type definition.
type TypeDef struct {
	// Index is the 1-based row number in the TypeDef table.
	Index      int
	Flags      uint32
	Name       string
	Namespace  string
	Extends    TypeRef
	FieldFrom  int
	FieldTo    int
	MethodFrom int
	MethodTo   int
}

// FullName returns the namespace-qualified name.
func (t TypeDef) FullName() string {
	if t.Namespace == "" {
		return t.Name
	}
	return t.Namespace + "." + t.Name
}

// IsInterface reports whether the type is an interface.
func (t TypeDef) IsInterface() bool { return t.Flags&0x20 != 0 }

// TypeRef points at a type in one of three tables.
type TypeRef struct {
	Table int
	Index uint32
}

// Method describes a method definition.
type Method struct {
	// Row is the 1-based row number in the MethodDef table.
	Row   int
	Flags uint16
	Name  string
	Sig   Signature
}

// Field describes a field definition.
type Field struct {
	// Row is the 1-based row number in the Field table.
	Row   int
	Flags uint16
	Name  string
	Sig   Signature
}

// InterfaceImpl describes one interface implemented by a type.
type InterfaceImpl struct {
	Class     uint32
	Interface TypeRef
}

// TypeDefs returns every type definition in the file, ordered by table row.
func (f *File) TypeDefs() []TypeDef {
	t := f.tables
	out := make([]TypeDef, 0, t.rows[tblTypeDef])
	for i := 1; i <= int(t.rows[tblTypeDef]); i++ {
		row := t.row(tblTypeDef, i)
		c := &rowCursor{data: row}
		def := TypeDef{
			Index:      i,
			Flags:      c.u4(),
			Name:       f.String(c.index(t.heapIndexSize(colString))),
			Namespace:  f.String(c.index(t.heapIndexSize(colString))),
			Extends:    readTypeDefOrRef(c, t),
			FieldFrom:  int(c.index(t.indexSize(tblField))),
			MethodFrom: int(c.index(t.indexSize(tblMethodDef))),
		}
		def.FieldTo = def.FieldFrom
		def.MethodTo = def.MethodFrom
		out = append(out, def)
	}

	// A type's field and method lists run until the next type's list starts.
	for i := range out {
		if i+1 < len(out) {
			out[i].FieldTo = out[i+1].FieldFrom
			out[i].MethodTo = out[i+1].MethodFrom
		} else {
			out[i].FieldTo = int(t.rows[tblField]) + 1
			out[i].MethodTo = int(t.rows[tblMethodDef]) + 1
		}
	}
	return out
}

// Methods returns the methods declared by a type, in vtable order.
func (f *File) Methods(def TypeDef) []Method {
	t := f.tables
	out := make([]Method, 0, def.MethodTo-def.MethodFrom)
	for i := def.MethodFrom; i < def.MethodTo; i++ {
		row := t.row(tblMethodDef, i)
		if row == nil {
			continue
		}
		c := &rowCursor{data: row}
		_ = c.u4() // RVA
		_ = c.u2() // ImplFlags
		flags := c.u2()
		name := f.String(c.index(t.heapIndexSize(colString)))
		sigBlob := f.BlobAt(c.index(t.heapIndexSize(colBlob)))
		_ = c.index(t.indexSize(tblParam)) // ParamList

		out = append(out, Method{
			Row:   i,
			Flags: flags,
			Name:  name,
			Sig:   parseMethodSignature(sigBlob),
		})
	}
	return out
}

// Fields returns the fields declared by a type, in declaration order.
func (f *File) Fields(def TypeDef) []Field {
	t := f.tables
	out := make([]Field, 0, def.FieldTo-def.FieldFrom)
	for i := def.FieldFrom; i < def.FieldTo; i++ {
		row := t.row(tblField, i)
		if row == nil {
			continue
		}
		c := &rowCursor{data: row}
		flags := c.u2()
		name := f.String(c.index(t.heapIndexSize(colString)))
		sigBlob := f.BlobAt(c.index(t.heapIndexSize(colBlob)))
		out = append(out, Field{
			Row:   i,
			Flags: flags,
			Name:  name,
			Sig:   parseFieldSignature(sigBlob),
		})
	}
	return out
}

// InterfaceImpls returns the interface implementation table.
func (f *File) InterfaceImpls() []InterfaceImpl {
	t := f.tables
	out := make([]InterfaceImpl, 0, t.rows[tblInterfaceImpl])
	for i := 1; i <= int(t.rows[tblInterfaceImpl]); i++ {
		row := t.row(tblInterfaceImpl, i)
		c := &rowCursor{data: row}
		out = append(out, InterfaceImpl{
			Class:     c.index(t.indexSize(tblTypeDef)),
			Interface: readTypeDefOrRef(c, t),
		})
	}
	return out
}

// readTypeDefOrRef reads a TypeDefOrRef coded index.
func readTypeDefOrRef(c *rowCursor, t *tableStream) TypeRef {
	width := t.codedSize(ciTypeDefOrRef)
	raw := c.index(width)
	tag := int(raw & ((1 << codedIndexes[ciTypeDefOrRef].tagBits) - 1))
	idx := raw >> codedIndexes[ciTypeDefOrRef].tagBits
	tbl := codedIndexes[ciTypeDefOrRef].tables[tag]
	return TypeRef{Table: tbl, Index: idx}
}

// ResolveTypeName returns the name of a type reference.
func (f *File) ResolveTypeName(ref TypeRef) string {
	switch ref.Table {
	case tblTypeDef:
		defs := f.TypeDefs()
		if int(ref.Index) >= 1 && int(ref.Index) <= len(defs) {
			return defs[ref.Index-1].FullName()
		}
	case tblTypeRef:
		row := f.tables.row(tblTypeRef, int(ref.Index))
		if row == nil {
			return ""
		}
		c := &rowCursor{data: row}
		_ = c.index(f.tables.codedSize(ciResolutionScope))
		name := f.String(c.index(f.tables.heapIndexSize(colString)))
		ns := f.String(c.index(f.tables.heapIndexSize(colString)))
		if ns == "" {
			return name
		}
		return ns + "." + name
	case tblTypeSpec:
		blob := f.BlobAt(ref.Index)
		sig := parseTypeSpecSignature(blob)
		return sig.String()
	}
	return ""
}

// FindTypeDef returns the type definition with the given full name.
func (f *File) FindTypeDef(fullName string) (TypeDef, bool) {
	for _, def := range f.TypeDefs() {
		if def.FullName() == fullName {
			return def, true
		}
	}
	return TypeDef{}, false
}

// Namespaces lists the namespaces present in the file, sorted.
func (f *File) Namespaces() []string {
	seen := map[string]bool{}
	for _, def := range f.TypeDefs() {
		if def.Namespace != "" {
			seen[def.Namespace] = true
		}
	}
	out := make([]string, 0, len(seen))
	for ns := range seen {
		out = append(out, ns)
	}
	sort.Strings(out)
	return out
}

// TypeDefsIn returns the type definitions in a namespace, sorted by name.
func (f *File) TypeDefsIn(namespace string) []TypeDef {
	var out []TypeDef
	for _, def := range f.TypeDefs() {
		if def.Namespace == namespace {
			out = append(out, def)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// String renders a dotted signature for diagnostics.
func (s Signature) String() string {
	var b strings.Builder
	b.WriteString(s.Return.String())
	b.WriteString(" (")
	for i, p := range s.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p.String())
	}
	b.WriteString(")")
	return b.String()
}
