package win

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	shgfiIcon      = 0x000000100
	shgfiLargeIcon = 0x000000000
)

type shFileInfo struct {
	HIcon       windows.Handle
	IconIndex   int32
	Attributes  uint32
	DisplayName [260]uint16
	TypeName    [80]uint16
}

type iconInfo struct {
	IsIcon   int32
	XHotspot uint32
	YHotspot uint32
	HbmMask  windows.Handle
	HbmColor windows.Handle
}

type bitmap struct {
	Type       int32
	Width      int32
	Height     int32
	WidthBytes int32
	Planes     uint16
	BitsPixel  uint16
	Bits       uintptr
}

var (
	iconModShell32 = windows.NewLazySystemDLL("shell32.dll")
	iconModUser32  = windows.NewLazySystemDLL("user32.dll")
	iconModGdi32   = windows.NewLazySystemDLL("gdi32.dll")

	procSHGetFileInfoW     = iconModShell32.NewProc("SHGetFileInfoW")
	procGetIconInfo        = iconModUser32.NewProc("GetIconInfo")
	procGetObjectW         = iconModGdi32.NewProc("GetObjectW")
	procGetDIBits          = iconModGdi32.NewProc("GetDIBits")
	procCreateCompatibleDC = iconModGdi32.NewProc("CreateCompatibleDC")
	procDeleteDC           = iconModGdi32.NewProc("DeleteDC")
	procSelectObject       = iconModGdi32.NewProc("SelectObject")
	procDestroyIcon        = iconModUser32.NewProc("DestroyIcon")
	procDeleteObject       = iconModGdi32.NewProc("DeleteObject")
)

// iconCache caches extracted icons by executable path so repeated calls (one per window
// refresh, one per pin list) do not re-read the PE resources each time.
var (
	iconCacheMu sync.Mutex
	iconCache   = make(map[string]iconCacheEntry)
)

type iconCacheEntry struct {
	data string
	err  error
}

// ExtractIconBase64 extracts the icon from an executable path and returns a Base64 PNG data URI.
//
// Results are cached by path, including failures, so a path that has no usable icon is
// not retried on every refresh.
func ExtractIconBase64(exePath string) (string, error) {
	if exePath == "" {
		return "", fmt.Errorf("empty executable path")
	}

	iconCacheMu.Lock()
	if entry, ok := iconCache[exePath]; ok {
		iconCacheMu.Unlock()
		return entry.data, entry.err
	}
	iconCacheMu.Unlock()

	data, err := ExtractIconPNG(exePath)
	var result string
	if err == nil {
		result = "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	}

	iconCacheMu.Lock()
	iconCache[exePath] = iconCacheEntry{data: result, err: err}
	iconCacheMu.Unlock()

	return result, err
}

// ExtractIconPNG extracts the icon for an executable path and encodes it into PNG
// bytes.
//
// A shortcut is resolved to its target first. A shortcut's own icon is the target's
// icon with an arrow drawn over it, and that is the icon of the shortcut rather than
// the icon of the application.
//
// The executable's own icon is preferred over the shell's, because the shell's is
// 32 pixels and the executable carries a 256 pixel PNG of the real thing.
func ExtractIconPNG(exePath string) ([]byte, error) {
	// Clean up path: strip surrounding quotes and expand environment variables
	exePath = strings.Trim(exePath, `"'`)
	exePath = os.ExpandEnv(exePath)

	// Resolve relative command names to absolute paths
	if resolved, err := exec.LookPath(exePath); err == nil {
		exePath = resolved
	}

	// A shortcut points at the application; the application is what has the icon.
	target := exePath
	if IsShortcut(exePath) {
		if resolved, err := ResolveShortcut(exePath); err == nil && resolved != "" {
			target = resolved
		}
	}

	// The real icon, at a size worth having.
	if png, err := ExtractLargestIconPNG(target); err == nil {
		return png, nil
	}

	// The shell's icon, which is smaller. It still comes from the target rather than
	// the shortcut: the shell's icon for a shortcut is the target's icon with an
	// arrow drawn over it.
	return extractShellIconPNG(target)
}

// extractShellIconPNG asks the shell for an icon and encodes it into PNG bytes.
func extractShellIconPNG(exePath string) ([]byte, error) {
	ptr, err := windows.UTF16PtrFromString(exePath)
	if err != nil {
		return nil, err
	}

	var fi shFileInfo
	res, _, _ := procSHGetFileInfoW.Call(
		uintptr(unsafe.Pointer(ptr)),
		0,
		uintptr(unsafe.Pointer(&fi)),
		unsafe.Sizeof(fi),
		shgfiIcon|shgfiLargeIcon,
	)

	if res == 0 || fi.HIcon == 0 {
		return nil, fmt.Errorf("could not extract icon for %s", exePath)
	}
	defer procDestroyIcon.Call(uintptr(fi.HIcon))

	img, err := iconToImage(windows.Handle(fi.HIcon))
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func iconToImage(hIcon windows.Handle) (image.Image, error) {
	var ii iconInfo
	r, _, _ := procGetIconInfo.Call(uintptr(hIcon), uintptr(unsafe.Pointer(&ii)))
	if r == 0 {
		return nil, fmt.Errorf("GetIconInfo failed")
	}
	defer procDeleteObject.Call(uintptr(ii.HbmMask))
	if ii.HbmColor != 0 {
		defer procDeleteObject.Call(uintptr(ii.HbmColor))
	}

	var bm bitmap
	procGetObjectW.Call(
		uintptr(ii.HbmColor),
		unsafe.Sizeof(bm),
		uintptr(unsafe.Pointer(&bm)),
	)

	width := int(bm.Width)
	height := int(bm.Height)
	if width == 0 || height == 0 {
		width, height = 32, 32
	}

	hdc, _, _ := procCreateCompatibleDC.Call(0)
	if hdc == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC failed")
	}
	defer procDeleteDC.Call(hdc)

	type bmiHeader struct {
		Size          uint32
		Width         int32
		Height        int32
		Planes        uint16
		BitCount      uint16
		Compression   uint32
		SizeImage     uint32
		XPelsPerMeter int32
		YPelsPerMeter int32
		ClrUsed       uint32
		ClrImportant  uint32
	}

	var bmi struct {
		Header bmiHeader
		Colors [1]uint32
	}
	bmi.Header.Size = uint32(unsafe.Sizeof(bmi.Header))
	bmi.Header.Width = int32(width)
	bmi.Header.Height = int32(-height) // Top-down
	bmi.Header.Planes = 1
	bmi.Header.BitCount = 32
	bmi.Header.Compression = 0

	pixels := make([]byte, width*height*4)
	oldObj, _, _ := procSelectObject.Call(hdc, uintptr(ii.HbmColor))

	// Only the top half is read: the bottom half of an icon's colour bitmap is
	// the mask, and reading it would put a second copy of the mask into the image.
	procGetDIBits.Call(
		hdc,
		uintptr(ii.HbmColor),
		0,
		uintptr(height),
		uintptr(unsafe.Pointer(&pixels[0])),
		uintptr(unsafe.Pointer(&bmi)),
		0,
	)

	if oldObj != 0 {
		procSelectObject.Call(hdc, oldObj)
	}

	// The transparency for icons without an alpha channel comes from the AND
	// mask. A set bit in the mask means the pixel is see-through.
	//
	// The mask is authoritative even for a 32 bit colour bitmap. Plenty of icons
	// are 32 bit with an alpha byte that is not used at all, and their shape
	// lives in the mask: reading the alpha instead leaves the icon on a black
	// rectangle.
	mask := readIconMask(ii.HbmMask, width, height)

	// The mask only gets to decide when it has something to say.
	useMask := false
	for _, v := range mask {
		if v != 0 {
			useMask = true
			break
		}
	}

	// And an image with no usable transparency anywhere is opaque.
	anyAlpha := false
	for i := 3; i < len(pixels); i += 4 {
		if pixels[i] != 0 {
			anyAlpha = true
			break
		}
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := (y*width + x) * 4
			b := pixels[i]
			g := pixels[i+1]
			r := pixels[i+2]
			a := pixels[i+3]

			switch {
			case useMask:
				if mask[y*width+x] != 0 {
					a = 0
				} else {
					a = 255
				}
			case !anyAlpha:
				a = 255
			}

			// A transparent pixel keeps its transparency. Turning it opaque is what
			// put a black rectangle behind every icon.
			img.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: a})
		}
	}

	return img, nil
}

// readIconMask reads the AND mask of an icon, one bit per pixel.
//
// The mask is a one bit bitmap whose rows are padded to four bytes, and a set bit
// means the pixel is see-through.
func readIconMask(hbmMask windows.Handle, width, height int) []byte {
	if hbmMask == 0 {
		return nil
	}

	var bm bitmap
	procGetObjectW.Call(
		uintptr(hbmMask),
		unsafe.Sizeof(bm),
		uintptr(unsafe.Pointer(&bm)),
	)

	maskWidth := int(bm.Width)
	maskHeight := int(bm.Height)
	if maskWidth <= 0 || maskHeight <= 0 {
		return nil
	}

	// Rows are padded to a four byte boundary.
	stride := (maskWidth + 31) / 32 * 4
	if maskHeight > height {
		maskHeight = height
	}

	type bmiHeader struct {
		Size          uint32
		Width         int32
		Height        int32
		Planes        uint16
		BitCount      uint16
		Compression   uint32
		SizeImage     uint32
		XPelsPerMeter int32
		YPelsPerMeter int32
		ClrUsed       uint32
		ClrImportant  uint32
	}

	var bmi struct {
		Header bmiHeader
		Colors [1]uint32
	}

	bmi.Header.Size = uint32(unsafe.Sizeof(bmi.Header))
	bmi.Header.Width = int32(maskWidth)
	bmi.Header.Height = int32(-maskHeight)
	bmi.Header.Planes = 1
	bmi.Header.BitCount = 1
	bmi.Header.Compression = 0

	hdc, _, _ := procCreateCompatibleDC.Call(0)
	if hdc == 0 {
		return nil
	}
	defer procDeleteDC.Call(hdc)

	oldObj, _, _ := procSelectObject.Call(hdc, uintptr(hbmMask))
	defer func() {
		if oldObj != 0 {
			procSelectObject.Call(hdc, oldObj)
		}
	}()

	bits := make([]byte, stride*maskHeight)
	procGetDIBits.Call(
		hdc,
		uintptr(hbmMask),
		0,
		uintptr(maskHeight),
		uintptr(unsafe.Pointer(&bits[0])),
		uintptr(unsafe.Pointer(&bmi)),
		0,
	)

	// The result is one byte per pixel: non-zero where the pixel is see-through.
	out := make([]byte, width*height)
	for y := 0; y < height && y < maskHeight; y++ {
		for x := 0; x < width && x < maskWidth; x++ {
			bit := bits[y*stride+x/8] & (1 << uint(x%8))
			if bit != 0 {
				out[y*width+x] = 1
			}
		}
	}
	return out
}
