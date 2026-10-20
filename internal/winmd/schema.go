package winmd

// This file describes the ECMA-335 metadata table layout.
//
// Only the parts needed to read a .winmd are modelled: table column schemas, the
// coded index encodings, and the rules for how wide an index column is.
//
// The widths matter. A table's row size is the sum of its column widths, and every
// table after it in the stream starts where the previous one ended. Getting one width
// wrong shifts every following table, so the rules are applied mechanically rather
// than by hand.

// column kinds.
const (
	colU1 colKind = iota
	colU2
	colU4
	colString
	colGUID
	colBlob
	colTable
	colCoded
)

type colKind uint8

// column describes one column of a metadata table.
type column struct {
	kind  colKind
	table int // for colTable
	coded int // for colCoded
}

// codedIndex describes a coded index: a tag of tagBits bits followed by a row index
// into one of the candidate tables, selected by the tag.
type codedIndex struct {
	tagBits uint
	tables  []int
}

// Metadata table numbers.
const (
	tblModule            = 0x00
	tblTypeRef           = 0x01
	tblTypeDef           = 0x02
	tblFieldPtr          = 0x03
	tblField             = 0x04
	tblMethodPtr         = 0x05
	tblMethodDef         = 0x06
	tblParamPtr          = 0x07
	tblParam             = 0x08
	tblInterfaceImpl     = 0x09
	tblMemberRef         = 0x0A
	tblConstant          = 0x0B
	tblCustomAttribute   = 0x0C
	tblFieldMarshal      = 0x0D
	tblDeclSecurity      = 0x0E
	tblClassLayout       = 0x0F
	tblFieldLayout       = 0x10
	tblStandAloneSig     = 0x11
	tblEventMap          = 0x12
	tblEventPtr          = 0x13
	tblEvent             = 0x14
	tblPropertyMap       = 0x15
	tblPropertyPtr       = 0x16
	tblProperty          = 0x17
	tblMethodSemantics   = 0x18
	tblMethodImpl        = 0x19
	tblModuleRef         = 0x1A
	tblTypeSpec          = 0x1B
	tblImplMap           = 0x1C
	tblFieldRVA          = 0x1D
	tblAssembly          = 0x1E
	tblAssemblyProcessor = 0x1F
	tblAssemblyOS        = 0x20
	tblAssemblyRef       = 0x21
	tblAssemblyRefProc   = 0x22
	tblAssemblyRefOS     = 0x23
	tblFile              = 0x24
	tblExportedType      = 0x25
	tblManifestResource  = 0x26
	tblNestedClass       = 0x27
	tblGenericParam      = 0x28
	tblMethodSpec        = 0x29
	tblGenericParamCons  = 0x2A
)

// Coded index numbers.
const (
	ciTypeDefOrRef = iota
	ciHasConstant
	ciHasCustomAttribute
	ciHasFieldMarshal
	ciHasDeclSecurity
	ciMemberRefParent
	ciHasSemantics
	ciMethodDefOrRef
	ciMemberForwarded
	ciImplementation
	ciCustomAttributeType
	ciResolutionScope
	ciTypeOrMethodDef
)

// codedIndexes lists the coded index encodings in ECMA-335 order.
var codedIndexes = []codedIndex{
	ciTypeDefOrRef: {tagBits: 2, tables: []int{tblTypeDef, tblTypeRef, tblTypeSpec}},
	ciHasConstant:  {tagBits: 2, tables: []int{tblField, tblParam, tblProperty}},
	ciHasCustomAttribute: {tagBits: 5, tables: []int{
		tblMethodDef, tblField, tblTypeRef, tblTypeDef, tblParam, tblInterfaceImpl,
		tblMemberRef, tblModule, tblDeclSecurity, tblProperty, tblEvent,
		tblStandAloneSig, tblModuleRef, tblTypeSpec, tblAssembly, tblAssemblyRef,
		tblFile, tblExportedType, tblManifestResource, tblGenericParam,
		tblGenericParamCons, tblMethodSpec,
	}},
	ciHasFieldMarshal:     {tagBits: 1, tables: []int{tblField, tblParam}},
	ciHasDeclSecurity:     {tagBits: 2, tables: []int{tblTypeDef, tblMethodDef, tblAssembly}},
	ciMemberRefParent:     {tagBits: 3, tables: []int{tblTypeDef, tblTypeRef, tblModuleRef, tblMethodDef, tblTypeSpec}},
	ciHasSemantics:        {tagBits: 1, tables: []int{tblEvent, tblProperty}},
	ciMethodDefOrRef:      {tagBits: 1, tables: []int{tblMethodDef, tblMemberRef}},
	ciMemberForwarded:     {tagBits: 1, tables: []int{tblField, tblMethodDef}},
	ciImplementation:      {tagBits: 2, tables: []int{tblFile, tblAssemblyRef, tblExportedType}},
	ciCustomAttributeType: {tagBits: 3, tables: []int{0xFF, 0xFF, tblMethodDef, tblMemberRef, 0xFF}},
	ciResolutionScope:     {tagBits: 2, tables: []int{tblModule, tblModuleRef, tblAssemblyRef, tblTypeRef}},
	ciTypeOrMethodDef:     {tagBits: 1, tables: []int{tblTypeDef, tblMethodDef}},
}

// tables lists every metadata table and its columns, in table-number order.
//
// A table that is absent from a file still needs a schema, because the reader walks
// the tables stream sequentially and must know how far each one reaches.
var tables = [][]column{
	tblModule: {
		{kind: colU2}, {kind: colString}, {kind: colGUID}, {kind: colGUID}, {kind: colGUID},
	},
	tblTypeRef: {
		{kind: colCoded, coded: ciResolutionScope}, {kind: colString}, {kind: colString},
	},
	tblTypeDef: {
		{kind: colU4}, {kind: colString}, {kind: colString},
		{kind: colCoded, coded: ciTypeDefOrRef},
		{kind: colTable, table: tblField}, {kind: colTable, table: tblMethodDef},
	},
	tblFieldPtr:  {{kind: colTable, table: tblField}},
	tblField:     {{kind: colU2}, {kind: colString}, {kind: colBlob}},
	tblMethodPtr: {{kind: colTable, table: tblMethodDef}},
	tblMethodDef: {
		{kind: colU4}, {kind: colU2}, {kind: colU2}, {kind: colString},
		{kind: colBlob}, {kind: colTable, table: tblParam},
	},
	tblParamPtr: {{kind: colTable, table: tblParam}},
	tblParam:    {{kind: colU2}, {kind: colU2}, {kind: colString}},
	tblInterfaceImpl: {
		{kind: colTable, table: tblTypeDef}, {kind: colCoded, coded: ciTypeDefOrRef},
	},
	tblMemberRef: {
		{kind: colCoded, coded: ciMemberRefParent}, {kind: colString}, {kind: colBlob},
	},
	tblConstant: {
		{kind: colU1}, {kind: colU1}, {kind: colCoded, coded: ciHasConstant}, {kind: colBlob},
	},
	tblCustomAttribute: {
		{kind: colCoded, coded: ciHasCustomAttribute},
		{kind: colCoded, coded: ciCustomAttributeType},
		{kind: colBlob},
	},
	tblFieldMarshal: {
		{kind: colCoded, coded: ciHasFieldMarshal}, {kind: colBlob},
	},
	tblDeclSecurity: {
		{kind: colU2}, {kind: colCoded, coded: ciHasDeclSecurity}, {kind: colBlob},
	},
	tblClassLayout: {
		{kind: colU2}, {kind: colU4}, {kind: colTable, table: tblTypeDef},
	},
	tblFieldLayout: {
		{kind: colU4}, {kind: colTable, table: tblField},
	},
	tblStandAloneSig: {{kind: colBlob}},
	tblEventMap: {
		{kind: colTable, table: tblTypeDef}, {kind: colTable, table: tblEvent},
	},
	tblEventPtr: {{kind: colTable, table: tblEvent}},
	tblEvent:    {{kind: colU2}, {kind: colString}, {kind: colCoded, coded: ciTypeDefOrRef}},
	tblPropertyMap: {
		{kind: colTable, table: tblTypeDef}, {kind: colTable, table: tblProperty},
	},
	tblPropertyPtr: {{kind: colTable, table: tblProperty}},
	tblProperty:    {{kind: colU2}, {kind: colString}, {kind: colBlob}},
	tblMethodSemantics: {
		{kind: colU2}, {kind: colTable, table: tblMethodDef}, {kind: colCoded, coded: ciHasSemantics},
	},
	tblMethodImpl: {
		{kind: colTable, table: tblTypeDef},
		{kind: colCoded, coded: ciMethodDefOrRef},
		{kind: colCoded, coded: ciMethodDefOrRef},
	},
	tblModuleRef: {{kind: colString}},
	tblTypeSpec:  {{kind: colBlob}},
	tblImplMap: {
		{kind: colU2}, {kind: colCoded, coded: ciMemberForwarded}, {kind: colString},
		{kind: colTable, table: tblModuleRef},
	},
	tblFieldRVA: {
		{kind: colU4}, {kind: colTable, table: tblField},
	},
	tblAssembly: {
		{kind: colU4}, {kind: colU2}, {kind: colU2}, {kind: colU2}, {kind: colU2},
		{kind: colU4}, {kind: colBlob}, {kind: colString}, {kind: colString},
	},
	tblAssemblyProcessor: {{kind: colU4}},
	tblAssemblyOS: {
		{kind: colU4}, {kind: colU4}, {kind: colU4},
	},
	tblAssemblyRef: {
		{kind: colU2}, {kind: colU2}, {kind: colU2}, {kind: colU2}, {kind: colU4},
		{kind: colBlob}, {kind: colString}, {kind: colString}, {kind: colBlob},
	},
	tblAssemblyRefProc: {
		{kind: colU4}, {kind: colTable, table: tblAssemblyRef},
	},
	tblAssemblyRefOS: {
		{kind: colU4}, {kind: colU4}, {kind: colU4}, {kind: colTable, table: tblAssemblyRef},
	},
	tblFile: {
		{kind: colU4}, {kind: colString}, {kind: colBlob},
	},
	tblExportedType: {
		{kind: colU4}, {kind: colU4}, {kind: colString}, {kind: colString},
		{kind: colCoded, coded: ciImplementation},
	},
	tblManifestResource: {
		{kind: colU4}, {kind: colU4}, {kind: colString}, {kind: colCoded, coded: ciImplementation},
	},
	tblNestedClass: {
		{kind: colTable, table: tblTypeDef}, {kind: colTable, table: tblTypeDef},
	},
	tblGenericParam: {
		{kind: colU2}, {kind: colU2}, {kind: colCoded, coded: ciTypeOrMethodDef}, {kind: colString},
	},
	tblMethodSpec: {
		{kind: colCoded, coded: ciMethodDefOrRef}, {kind: colBlob},
	},
	tblGenericParamCons: {
		{kind: colTable, table: tblGenericParam}, {kind: colCoded, coded: ciTypeDefOrRef},
	},
}
