package diagnostics

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFaultMetadataDoesNotExposePaths(t *testing.T) {
	raw := []byte(`<Event><System><TimeCreated SystemTime="2026-11-02T12:06:15Z"/></System><EventData><Data Name="AppName">Qman-synthetic.exe</Data><Data Name="ModuleName">Qman-synthetic.exe</Data><Data Name="ExceptionCode">c0000005</Data><Data Name="FaultingOffset">00081aa7</Data><Data Name="AppPath">C:\private-account\media</Data></EventData></Event>`)
	fault, ok := parseFault(raw)
	if !ok || fault.Code != "c0000005" {
		t.Fatal("fault not parsed")
	}
	b, _ := json.Marshal(fault)
	if strings.Contains(string(b), "private-account") {
		t.Fatal("private path leaked")
	}
	raw = []byte(strings.ReplaceAll(string(raw), "Qman-synthetic.exe", "other.exe"))
	if _, ok = parseFault(raw); ok {
		t.Fatal("unrelated application included")
	}
}
