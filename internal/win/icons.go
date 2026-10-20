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

// ExtractIconBase64 extracts the icon from an executable path and returns a Base64 PNG data URI.
func ExtractIconBase64(exePath string) (string, error) {
	data, err := ExtractIconPNG(exePath)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data), nil
}

// ExtractIconPNG extracts an HICON and encodes it into PNG bytes.
func ExtractIconPNG(exePath string) ([]byte, error) {
	// Clean up path: strip surrounding quotes and expand environment variables
	exePath = strings.Trim(exePath, `"'`)
	exePath = os.ExpandEnv(exePath)

	// Resolve relative command names to absolute paths
	if resolved, err := exec.LookPath(exePath); err == nil {
		exePath = resolved
	}

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

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := (y*width + x) * 4
			b := pixels[i]
			g := pixels[i+1]
			r := pixels[i+2]
			a := pixels[i+3]
			if a == 0 {
				a = 255
			}
			img.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: a})
		}
	}

	return img, nil
}
