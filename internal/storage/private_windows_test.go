package storage

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateAndNoOverwrite(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	if err := Private(root); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	sddl := sd.String()
	for _, broad := range []string{";;;WD)", ";;;BU)", ";;;AU)", ";;;AC)", ";;;S-1-15-2-1)"} {
		if strings.Contains(sddl, broad) {
			t.Fatal("overly broad private access")
		}
	}
	a, b := filepath.Join(root, "stage"), filepath.Join(root, "existing")
	os.WriteFile(a, []byte("new"), 0600)
	os.WriteFile(b, []byte("old"), 0600)
	if err = Publish(a, b); err == nil {
		t.Fatal("overwrote existing output")
	}
	contents, _ := os.ReadFile(b)
	if string(contents) != "old" {
		t.Fatal("existing output changed")
	}
}
