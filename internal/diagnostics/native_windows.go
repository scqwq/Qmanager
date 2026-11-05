package diagnostics

import (
	"encoding/xml"
	"golang.org/x/sys/windows"
	"strings"
	"time"
	"unsafe"
)

var eventDLL = windows.NewLazySystemDLL("wevtapi.dll")

type faultXML struct {
	System struct {
		Time struct {
			At string `xml:"SystemTime,attr"`
		} `xml:"TimeCreated"`
	} `xml:"System"`
	Data []struct {
		Name  string `xml:"Name,attr"`
		Value string `xml:",chardata"`
	} `xml:"EventData>Data"`
}

func parseFault(b []byte) (Crash, bool) {
	var x faultXML
	if xml.Unmarshal(b, &x) != nil {
		return Crash{}, false
	}
	d := map[string]string{}
	for _, v := range x.Data {
		d[v.Name] = v.Value
	}
	app := d["AppName"]
	name := strings.ToLower(app)
	if (name != "qman.exe" && !strings.HasPrefix(name, "qman-")) || !strings.HasSuffix(name, ".exe") || strings.ContainsAny(app, "/\\") {
		return Crash{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, x.System.Time.At)
	if err != nil {
		return Crash{}, false
	}
	return Crash{At: at, App: clipped(app, 100), Module: clipped(d["ModuleName"], 100), Code: clipped(d["ExceptionCode"], 32), Offset: clipped(d["FaultingOffset"], 32)}, true
}

// Read only recent Application Error metadata. No event message/path, process
// memory, registry changes, elevated helper or dump collection is involved.
func systemCrashes() ([]Crash, string) {
	result := []Crash{}
	path, _ := windows.UTF16PtrFromString("Application")
	query, _ := windows.UTF16PtrFromString("*[System[(EventID=1000) and TimeCreated[timediff(@SystemTime) <= 604800000]]]")
	h, _, _ := eventDLL.NewProc("EvtQuery").Call(0, uintptr(unsafe.Pointer(path)), uintptr(unsafe.Pointer(query)), 0x201)
	if h == 0 {
		return result, "Windows 崩溃记录暂不可读取；本地运行日志仍可查看。"
	}
	closeEvent := eventDLL.NewProc("EvtClose")
	defer closeEvent.Call(h)
	for i := 0; i < 64; i++ {
		var event uintptr
		var count uint32
		ok, _, e := eventDLL.NewProc("EvtNext").Call(h, 1, uintptr(unsafe.Pointer(&event)), 100, 0, uintptr(unsafe.Pointer(&count)))
		if ok == 0 {
			if e != windows.ERROR_NO_MORE_ITEMS {
				return result, "Windows 崩溃记录读取未完成。"
			}
			break
		}
		b := make([]uint16, 64<<10)
		var used, props uint32
		ok, _, _ = eventDLL.NewProc("EvtRender").Call(0, event, 1, uintptr(len(b)*2), uintptr(unsafe.Pointer(&b[0])), uintptr(unsafe.Pointer(&used)), uintptr(unsafe.Pointer(&props)))
		closeEvent.Call(event)
		if ok != 0 {
			if fault, valid := parseFault([]byte(windows.UTF16ToString(b))); valid {
				result = append(result, fault)
			}
		}
	}
	return result, ""
}
