package win

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
)

// Extracting the real icon out of an executable.
//
// The shell hands over a 32 pixel icon, which is what every icon is scaled down
// from on the desktop as well. The executable carries the real thing: a 256 pixel
// PNG in its resources. Reading it out is the difference between an icon that is
// sharp and one that is a blur, and the difference between an icon that is sharp
// at any size and one that has to be re-rendered.
//
// This is a small PE reader, not a resource library. It reads the resource
// directory, looks for the PNG icons in it, and hands back the largest.

// pngSignature is the first eight bytes of a PNG file.
var pngSignature = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}

// Resource directory types.
const (
	// resourceTypeIcon is RT_ICON, the icon images themselves.
	resourceTypeIcon = 3
)

// peSection is one section of the executable's section table.
type peSection struct {
	virtualAddress uint32
	virtualSize    uint32
	rawPointer     uint32
	rawSize        uint32
}

// peImage is the parts of a PE file the icon reader needs.
type peImage struct {
	data         []byte
	sections     []peSection
	resourceRVA  uint32
	resourceSize uint32
}

// openPE reads a PE image and its resource directory.
func openPE(path string) (*peImage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 0x40 || !bytes.Equal(data[:2], []byte("MZ")) {
		return nil, fmt.Errorf("%s is not a PE image", path)
	}

	peOffset := int(binary.LittleEndian.Uint32(data[0x3C:0x40]))
	if peOffset <= 0 || peOffset+24 > len(data) {
		return nil, fmt.Errorf("%s has no PE header", path)
	}
	if !bytes.Equal(data[peOffset:peOffset+4], []byte("PE\x00\x00")) {
		return nil, fmt.Errorf("%s has no PE signature", path)
	}

	coff := peOffset + 4
	sectionCount := int(binary.LittleEndian.Uint16(data[coff+2:]))
	optionalSize := int(binary.LittleEndian.Uint16(data[coff+16:]))

	optional := coff + 20
	magic := binary.LittleEndian.Uint16(data[optional:])
	var directoryOffset int
	switch magic {
	case 0x10b: // PE32
		directoryOffset = optional + 96
	case 0x20b: // PE32+
		directoryOffset = optional + 112
	default:
		return nil, fmt.Errorf("%s has an unknown optional header", path)
	}

	image := &peImage{data: data}

	// The resource directory is entry 2 of the data directory table.
	if directoryOffset+3*8 > len(data) {
		return nil, fmt.Errorf("%s has no data directory", path)
	}
	image.resourceRVA = binary.LittleEndian.Uint32(data[directoryOffset+2*8:])
	image.resourceSize = binary.LittleEndian.Uint32(data[directoryOffset+2*8+4:])

	sectionTable := optional + optionalSize
	for i := 0; i < sectionCount; i++ {
		base := sectionTable + i*40
		if base+40 > len(data) {
			return nil, fmt.Errorf("%s has a truncated section table", path)
		}
		image.sections = append(image.sections, peSection{
			virtualSize:    binary.LittleEndian.Uint32(data[base+8:]),
			virtualAddress: binary.LittleEndian.Uint32(data[base+12:]),
			rawSize:        binary.LittleEndian.Uint32(data[base+16:]),
			rawPointer:     binary.LittleEndian.Uint32(data[base+20:]),
		})
	}

	if image.resourceRVA == 0 {
		return nil, fmt.Errorf("%s has no resources", path)
	}
	return image, nil
}

// rvaToOffset maps a relative virtual address to a file offset.
func (p *peImage) rvaToOffset(rva uint32) (int, bool) {
	for _, s := range p.sections {
		size := s.virtualSize
		if s.rawSize > size {
			size = s.rawSize
		}
		if rva >= s.virtualAddress && rva < s.virtualAddress+size {
			offset := int(s.rawPointer + (rva - s.virtualAddress))
			if offset < 0 || offset >= len(p.data) {
				return 0, false
			}
			return offset, true
		}
	}
	return 0, false
}

// resourceBase is the file offset of the resource directory.
func (p *peImage) resourceBase() (int, bool) {
	return p.rvaToOffset(p.resourceRVA)
}

// resourceEntries reads the entries of one resource directory level.
//
// The level's offset is relative to the root of the resource directory.
func (p *peImage) resourceEntries(base, offset int) ([]resourceEntry, bool) {
	if base+offset+16 > len(p.data) {
		return nil, false
	}
	dir := p.data[base+offset:]

	named := int(binary.LittleEndian.Uint16(dir[12:]))
	ids := int(binary.LittleEndian.Uint16(dir[14:]))
	count := named + ids
	if count < 0 || count > 4096 {
		return nil, false
	}

	out := make([]resourceEntry, 0, count)
	for i := 0; i < count; i++ {
		at := base + offset + 16 + i*8
		if at+8 > len(p.data) {
			return nil, false
		}
		out = append(out, resourceEntry{
			name:     binary.LittleEndian.Uint32(p.data[at:]),
			offset:   binary.LittleEndian.Uint32(p.data[at+4:]),
			isDir:    binary.LittleEndian.Uint32(p.data[at+4:])&0x80000000 != 0,
			offsetTo: binary.LittleEndian.Uint32(p.data[at+4:]) &^ 0x80000000,
		})
	}
	return out, true
}

// resourceEntry is one entry of a resource directory level.
type resourceEntry struct {
	name     uint32
	offset   uint32
	isDir    bool
	offsetTo uint32
}

// DebugIconResources lists the icon resources in an executable.
//
// It exists to answer one question when the extraction misbehaves: what is actually
// in there. An executable's icons are a handful of small colour bitmaps and, on a
// modern one, a PNG, and the difference between them is invisible until the
// extraction is wrong.
func DebugIconResources(path string) ([]string, error) {
	image, err := openPE(path)
	if err != nil {
		return nil, err
	}

	base, ok := image.resourceBase()
	if !ok {
		return nil, fmt.Errorf("%s has no readable resource directory", path)
	}

	var out []string
	types, ok := image.resourceEntries(base, 0)
	if !ok {
		return nil, fmt.Errorf("%s has an unreadable resource directory", path)
	}
	out = append(out, fmt.Sprintf("resource directory at %d, %d types", base, len(types)))

	for _, typeEntry := range types {
		name := "named"
		if typeEntry.name&0x80000000 == 0 {
			name = fmt.Sprintf("type %d", typeEntry.name)
		}
		out = append(out, fmt.Sprintf("  %s isDir=%v", name, typeEntry.isDir))
		if !typeEntry.isDir {
			continue
		}
		names, ok := image.resourceEntries(base, int(typeEntry.offsetTo))
		if !ok {
			continue
		}
		for _, nameEntry := range names {
			if !nameEntry.isDir {
				continue
			}
			languages, ok := image.resourceEntries(base, int(nameEntry.offsetTo))
			if !ok {
				continue
			}
			for _, languageEntry := range languages {
				if languageEntry.isDir {
					continue
				}
				out = append(out, fmt.Sprintf("    %s", image.describeIcon(base, int(languageEntry.offsetTo))))
			}
		}
	}
	return out, nil
}

// describeIcon describes one icon resource.
func (p *peImage) describeIcon(base, entryOffset int) string {
	if base+entryOffset+16 > len(p.data) {
		return "unreadable data entry"
	}
	rva := binary.LittleEndian.Uint32(p.data[base+entryOffset:])
	size := int(binary.LittleEndian.Uint32(p.data[base+entryOffset+4:]))

	offset, ok := p.rvaToOffset(rva)
	if !ok || offset+8 > len(p.data) {
		return fmt.Sprintf("rva %d size %d: unreadable", rva, size)
	}
	data := p.data[offset:]

	if len(data) >= 8 && bytes.Equal(data[:8], pngSignature) {
		if len(data) < 24 {
			return "png, truncated"
		}
		width := int(binary.BigEndian.Uint32(data[16:]))
		height := int(binary.BigEndian.Uint32(data[20:]))
		return fmt.Sprintf("%dx%d png, %d bytes", width, height, size)
	}
	return fmt.Sprintf("bitmap, %d bytes", size)
}

// ExtractLargestIconPNG returns the largest PNG icon inside an executable.
//
// An executable usually carries several icons: a handful of small ones as colour
// bitmaps, and the modern one as a PNG. The PNGs are the good ones, so the largest
// PNG wins.
func ExtractLargestIconPNG(path string) ([]byte, error) {
	image, err := openPE(path)
	if err != nil {
		return nil, err
	}

	base, ok := image.resourceBase()
	if !ok {
		return nil, fmt.Errorf("%s has no readable resource directory", path)
	}

	types, ok := image.resourceEntries(base, 0)
	if !ok {
		return nil, fmt.Errorf("%s has an unreadable resource directory", path)
	}

	best := []byte(nil)
	bestSize := 0

	for _, typeEntry := range types {
		// The type is an identifier when the high bit is clear.
		if typeEntry.name&0x80000000 != 0 || typeEntry.name != resourceTypeIcon {
			continue
		}
		if !typeEntry.isDir {
			continue
		}

		names, ok := image.resourceEntries(base, int(typeEntry.offsetTo))
		if !ok {
			continue
		}

		for _, nameEntry := range names {
			if !nameEntry.isDir {
				continue
			}
			languages, ok := image.resourceEntries(base, int(nameEntry.offsetTo))
			if !ok {
				continue
			}
			for _, languageEntry := range languages {
				if languageEntry.isDir {
					continue
				}

				png, size, ok := image.iconPNG(base, int(languageEntry.offsetTo))
				if !ok {
					continue
				}
				if size > bestSize {
					best = png
					bestSize = size
				}
			}
		}
	}

	if best == nil {
		return nil, fmt.Errorf("%s has no PNG icon", path)
	}
	return best, nil
}

// iconPNG reads one icon resource and returns it if it is a PNG.
//
// A PNG icon's resource data is the PNG itself, with no header in front of it: the
// dimensions come from the group's entry, and the image is stored raw. The older
// colour bitmap icons start with a BITMAPINFOHEADER instead.
func (p *peImage) iconPNG(base, entryOffset int) ([]byte, int, bool) {
	// The data entry holds the RVA and the size of the resource.
	if base+entryOffset+16 > len(p.data) {
		return nil, 0, false
	}
	rva := binary.LittleEndian.Uint32(p.data[base+entryOffset:])
	size := int(binary.LittleEndian.Uint32(p.data[base+entryOffset+4:]))
	if size <= 0 {
		return nil, 0, false
	}

	offset, ok := p.rvaToOffset(rva)
	if !ok || offset+size > len(p.data) {
		return nil, 0, false
	}
	data := p.data[offset : offset+size]

	if len(data) < 24 || !bytes.Equal(data[:8], pngSignature) {
		return nil, 0, false
	}

	// The dimensions are in the PNG's first chunk: eight bytes of signature, a
	// four byte length, the "IHDR" type, and then the width and the height.
	width := int(binary.BigEndian.Uint32(data[16:]))
	height := int(binary.BigEndian.Uint32(data[20:]))
	if width <= 0 || height <= 0 {
		return nil, 0, false
	}

	return data, width * height, true
}
