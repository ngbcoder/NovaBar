package win

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
)

// Resolving a Windows shortcut.
//
// A shortcut's own icon is the target's icon with an arrow drawn over it, which is
// the wrong icon for a dock: it is the icon of the shortcut rather than the icon of
// the application. The target's icon is what the user means.
//
// The shortcut file says where it points. The format is the shell link binary file
// format, and the part that matters is small: a fixed size header, and then a link
// information block with the target's path in it.
//
// The shell will resolve a shortcut through its own interface as well, and that is
// the authority. It is not used here: the call crashed the process every time, and a
// parser that cannot crash is worth more than an authority that can.

// Shell link layout, in bytes.
const (
	// shellLinkHeaderSize is the size of the shell link header.
	shellLinkHeaderSize = 0x4C
	// linkInfoHeaderSize is the size of the link information block's header.
	linkInfoHeaderSize = 0x1C
)

// Shell link flags.
const (
	// shellLinkHasLinkTargetIDList means the shortcut carries a shell link list
	// between the header and the link information.
	shellLinkHasLinkTargetIDList = 0x00000001
	// shellLinkHasLinkInfo means the shortcut carries a link information block.
	shellLinkHasLinkInfo = 0x00000002
)

// Offsets into the shell link header.
const (
	// shellLinkHeaderSizeOffset holds the size of the header.
	shellLinkHeaderSizeOffset = 0x00
	// shellLinkFlagsOffset holds the link flags.
	shellLinkFlagsOffset = 0x14
)

// Offsets into the link information block's header.
const (
	// linkInfoLocalBasePathOffset holds the offset of the local base path.
	linkInfoLocalBasePathOffset = 0x10
	// linkInfoCommonPathSuffixOffset holds the offset of the common path suffix.
	linkInfoCommonPathSuffixOffset = 0x18
)

// ResolveShortcut returns the path a shortcut points at.
//
// An empty string comes back with an error: a shortcut with no target is a broken
// shortcut, and the caller would rather fall back to the shell's icon than show
// nothing.
func ResolveShortcut(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) < shellLinkHeaderSize {
		return "", fmt.Errorf("%s is too short to be a shortcut", path)
	}

	headerSize := int(binary.LittleEndian.Uint32(data[shellLinkHeaderSizeOffset:]))
	if headerSize < shellLinkHeaderSize || headerSize > len(data) {
		return "", fmt.Errorf("%s has an unreadable header", path)
	}
	flags := binary.LittleEndian.Uint32(data[shellLinkFlagsOffset:])

	// What follows the header depends on the flags: a shell link list comes first
	// when the shortcut has one, and the link information block after that.
	offset := headerSize
	if flags&shellLinkHasLinkTargetIDList != 0 {
		if offset+2 > len(data) {
			return "", fmt.Errorf("%s has a truncated link list", path)
		}
		// The size in the list does not count the item count field, so the
		// structure is two bytes longer than it says.
		listSize := int(binary.LittleEndian.Uint16(data[offset:])) + 2
		if listSize < 4 || offset+listSize > len(data) {
			return "", fmt.Errorf("%s has an unreadable link list", path)
		}
		offset += listSize
	}

	if flags&shellLinkHasLinkInfo == 0 {
		return "", fmt.Errorf("%s has no link information", path)
	}
	if offset+linkInfoHeaderSize > len(data) {
		return "", fmt.Errorf("%s has no link information", path)
	}

	localOffset := int(binary.LittleEndian.Uint32(data[offset+linkInfoLocalBasePathOffset:]))
	suffixOffset := int(binary.LittleEndian.Uint32(data[offset+linkInfoCommonPathSuffixOffset:]))

	local := readANSIString(data, offset+localOffset)
	suffix := readANSIString(data, offset+suffixOffset)
	if local == "" && suffix == "" {
		return "", fmt.Errorf("%s has no target", path)
	}

	// The local base path is the drive and the directories, and the common path
	// suffix is the file name. Together they are the target.
	target := local
	if suffix != "" {
		target = local + suffix
	}
	return target, nil
}

// readANSIString reads a null terminated single byte string from a buffer.
//
// The link information block holds its paths as single byte characters even in a
// shortcut that is otherwise Unicode. An offset past the end of the buffer gives an
// empty string rather than a panic: the file is not trusted, and a shortcut with a
// broken offset is a broken shortcut, not a reason to take the shell down.
func readANSIString(data []byte, offset int) string {
	if offset < 0 || offset >= len(data) {
		return ""
	}

	end := offset
	for end < len(data) && data[end] != 0 {
		end++
	}
	return string(data[offset:end])
}

// IsShortcut reports whether a path names a Windows shortcut.
func IsShortcut(path string) bool {
	return strings.EqualFold(pathExtension(path), ".lnk")
}

// pathExtension returns the extension of a path, in lower case.
func pathExtension(path string) string {
	if i := strings.LastIndexAny(path, `.\/`); i >= 0 {
		return path[i:]
	}
	return ""
}
