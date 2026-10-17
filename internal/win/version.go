package win

import (
	"fmt"
	"unsafe"
)

// Windows 11 starts at build 22000. Windows 10 tops out at 19045.
const (
	// MinimumSupportedBuild is the first Windows 11 build.
	MinimumSupportedBuild = 22000
	// MaximumWindows10Build is the highest Windows 10 build, used only to give a
	// clearer diagnostic than a bare "unsupported".
	MaximumWindows10Build = 19045
)

var procRtlGetVersion = modNtdll.NewProc("RtlGetVersion")

// rtlOSVersionInfoEx mirrors the RTL_OSVERSIONINFOEXW layout.
type rtlOSVersionInfoEx struct {
	OsVersionInfoSize uint32
	MajorVersion      uint32
	MinorVersion      uint32
	BuildNumber       uint32
	PlatformID        uint32
	CSDVersion        [128]uint16
	ServicePackMajor  uint16
	ServicePackMinor  uint16
	SuiteMask         uint16
	ProductType       byte
	Reserved          byte
}

// OsVersion describes the running Windows build.
type OsVersion struct {
	Major uint32
	Minor uint32
	Build uint32
}

// String renders the version for logs.
func (v OsVersion) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Build)
}

// IsSupported reports whether the build is Windows 11 or newer.
//
// NovaBar targets Windows 11 only. That is a deliberate scope decision, not an
// accident: WebView2 is guaranteed present on Windows 11, which removes the last open
// question about the M4 UI layer and means no WebView2Loader.dll needs shipping.
func (v OsVersion) IsSupported() bool {
	return v.Build >= MinimumSupportedBuild
}

// UnsupportedReason explains an unsupported build, or returns "" when supported.
func (v OsVersion) UnsupportedReason() string {
	if v.IsSupported() {
		return ""
	}
	if v.Build <= MaximumWindows10Build {
		return "NovaBar targets Windows 11 only; this looks like Windows 10 (" + v.String() +
			"). It may start, but WebView2 and other shell behaviour are not guaranteed."
	}
	return fmt.Sprintf("NovaBar targets Windows 11 or newer; detected build %s is older than the minimum of 10.0.%d.",
		v.String(), MinimumSupportedBuild)
}

// GetOsVersion returns the running Windows version.
//
// Uses RtlGetVersion rather than GetVersion/GetVersionEx, both of which are
// manifest-dependent: without a compatibility manifest on the executable, Windows
// reports 6.2 (Vista) for every modern build, which would make this check report every
// supported machine as unsupported.
func GetOsVersion() (OsVersion, error) {
	var info rtlOSVersionInfoEx
	info.OsVersionInfoSize = uint32(unsafe.Sizeof(info))

	r1, _, _ := procRtlGetVersion.Call(uintptr(unsafe.Pointer(&info)))
	// NTSTATUS success is 0.
	if int32(r1) != 0 {
		return OsVersion{}, fmt.Errorf("RtlGetVersion failed with status %#x", int32(r1))
	}

	return OsVersion{
		Major: info.MajorVersion,
		Minor: info.MinorVersion,
		Build: info.BuildNumber,
	}, nil
}
