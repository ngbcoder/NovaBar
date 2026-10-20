package winmd

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParseRejectsGarbage checks that a file which is not metadata is rejected
// rather than parsed into nonsense.
func TestParseRejectsGarbage(t *testing.T) {
	cases := [][]byte{
		nil,
		[]byte("not a PE image at all"),
		[]byte("MZ"),
	}

	for _, data := range cases {
		if _, err := Parse(data); err == nil {
			t.Errorf("Parse(%q) succeeded; it should have failed", string(data))
		}
	}
}

// TestReadCompressedUint pins the compressed integer encoding.
//
// The widths here decide where every table starts, so an error in this function
// shifts the whole file.
func TestReadCompressedUint(t *testing.T) {
	cases := []struct {
		data []byte
		want uint32
		size int
	}{
		{[]byte{0x03}, 3, 1},
		{[]byte{0x7F}, 0x7F, 1},
		// The two byte form carries six bits in the first byte and eight in the
		// second, so 0x80 0x01 is one, not 0x80.
		{[]byte{0x80, 0x01}, 1, 2},
		{[]byte{0xBF, 0xFF}, 0x3FFF, 2},
		{[]byte{0xC0, 0x00, 0x00, 0x00}, 0, 4},
		{[]byte{0xC0, 0x00, 0x01, 0x00}, 0x100, 4},
	}

	for _, tc := range cases {
		got, size := readCompressedUint(tc.data)
		if got != tc.want || size != tc.size {
			t.Errorf("readCompressedUint(%x) = %d, %d; want %d, %d", tc.data, got, size, tc.want, tc.size)
		}
	}
}

// metadataPath returns the path to a Windows metadata file, or "" when it is absent.
func metadataPath(name string) string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	path := filepath.Join(root, "System32", "WinMetadata", name)
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// TestParseWindowsMediaMetadata reads the media metadata the operating system
// ships and checks a few facts that the rest of the tree depends on.
//
// The identifiers and the method order in this file are what the media bridge
// hardcodes, so this is the test that would catch a parser that silently mis-reads
// the file rather than failing.
func TestParseWindowsMediaMetadata(t *testing.T) {
	path := metadataPath("Windows.Media.winmd")
	if path == "" {
		t.Skip("Windows.Media.winmd is not available on this machine")
	}

	f, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%s): %v", path, err)
	}

	// The namespaces must be readable, or nothing else is.
	namespaces := f.Namespaces()
	if len(namespaces) == 0 {
		t.Fatal("no namespaces were read")
	}

	// A known interface, and its identifier.
	const statics = "Windows.Media.Control.IGlobalSystemMediaTransportControlsSessionManagerStatics"

	def, ok := f.FindTypeDef(statics)
	if !ok {
		t.Fatalf("%s is missing from the metadata", statics)
	}
	if !def.IsInterface() {
		t.Errorf("%s should be an interface", statics)
	}

	guid, ok := f.TypeGUID(def)
	if !ok {
		t.Fatalf("%s has no identifier", statics)
	}
	if guid.String() != "{2050c4ee-11a0-57de-aed7-c97c70338245}" {
		t.Errorf("%s = %s", statics, guid)
	}

	// Its single method, and its order.
	methods := f.Methods(def)
	if len(methods) != 1 || methods[0].Name != "RequestAsync" {
		t.Errorf("%s methods = %v, want [RequestAsync]", statics, methodNames(methods))
	}
}

// TestParseWindowsFoundationMetadata reads the async interfaces.
func TestParseWindowsFoundationMetadata(t *testing.T) {
	path := metadataPath("Windows.Foundation.winmd")
	if path == "" {
		t.Skip("Windows.Foundation.winmd is not available on this machine")
	}

	f, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%s): %v", path, err)
	}

	def, ok := f.FindTypeDef("Windows.Foundation.IAsyncOperation`1")
	if !ok {
		t.Fatal("IAsyncOperation is missing from the metadata")
	}

	methods := f.Methods(def)
	want := []string{"put_Completed", "get_Completed", "GetResults"}
	if len(methods) != len(want) {
		t.Fatalf("IAsyncOperation methods = %v, want %v", methodNames(methods), want)
	}
	for i, name := range want {
		if methods[i].Name != name {
			t.Errorf("IAsyncOperation method %d = %q, want %q", i, methods[i].Name, name)
		}
	}
}

// methodNames lists the names of a set of methods.
func methodNames(methods []Method) []string {
	out := make([]string, 0, len(methods))
	for _, m := range methods {
		out = append(out, m.Name)
	}
	return out
}
