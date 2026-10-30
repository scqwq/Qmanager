// Package watch wraps bounded Windows subtree notifications. Events are hints;
// the application always reconciles them against a read-only inventory.
package watch

import (
	"context"
	"encoding/binary"
	"errors"
	"golang.org/x/sys/windows"
	"path/filepath"
	"qqarchive/internal/media"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"
)

type Event struct {
	Path   string
	Action uint32
	At     time.Time
	Gap    bool
}
type Watch struct {
	Events   chan Event
	dropped  atomic.Bool
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	Complete bool
}
type subscription struct {
	handle  windows.Handle
	event   windows.Handle
	overlap windows.Overlapped
	buffer  []byte
	prefix  string
}

func Start(parent context.Context, source media.Source) (*Watch, error) {
	ctx, cancel := context.WithCancel(parent)
	w := &Watch{Events: make(chan Event, 512), cancel: cancel, Complete: true}
	for _, scope := range source.Scopes {
		p, err := windows.UTF16PtrFromString(media.NativePath(scope.Path))
		if err != nil {
			w.Complete = false
			continue
		}
		h, err := windows.CreateFile(p, windows.FILE_LIST_DIRECTORY, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OVERLAPPED|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if err != nil {
			w.Complete = false
			continue
		}
		// Validate the root by the same handle-based read path used by the scanner.
		f, e := media.OpenRelative(scope.Path, ".")
		if e != nil {
			windows.CloseHandle(h)
			w.Complete = false
			continue
		}
		f.Close()
		event, err := windows.CreateEvent(nil, 1, 0, nil)
		if err != nil {
			windows.CloseHandle(h)
			w.Complete = false
			continue
		}
		s := &subscription{handle: h, event: event, buffer: make([]byte, 32768), prefix: scope.Prefix}
		s.overlap.HEvent = event
		// Arm synchronously before baseline enumeration starts.
		if err = s.arm(); err != nil {
			windows.CloseHandle(h)
			windows.CloseHandle(event)
			w.Complete = false
			continue
		}
		w.wg.Add(1)
		go w.run(ctx, s)
	}
	return w, nil
}
func (s *subscription) arm() error {
	windows.ResetEvent(s.event)
	s.overlap = windows.Overlapped{HEvent: s.event}
	err := windows.ReadDirectoryChanges(s.handle, &s.buffer[0], uint32(len(s.buffer)), true, windows.FILE_NOTIFY_CHANGE_FILE_NAME|windows.FILE_NOTIFY_CHANGE_DIR_NAME|windows.FILE_NOTIFY_CHANGE_SIZE|windows.FILE_NOTIFY_CHANGE_LAST_WRITE, nil, &s.overlap, 0)
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		return nil
	}
	return err
}
func (w *Watch) emit(e Event) {
	select {
	case w.Events <- e:
	default:
		w.dropped.Store(true)
	}
}
func (w *Watch) run(ctx context.Context, s *subscription) {
	defer w.wg.Done()
	defer windows.CloseHandle(s.event)
	defer windows.CloseHandle(s.handle)
	defer func() {
		windows.CancelIoEx(s.handle, &s.overlap)
		var n uint32
		windows.GetOverlappedResult(s.handle, &s.overlap, &n, true)
	}()
	for {
		if ctx.Err() != nil {
			return
		}
		status, err := windows.WaitForSingleObject(s.event, 250)
		if err != nil {
			w.emit(Event{At: time.Now().UTC(), Gap: true})
			return
		}
		if status == uint32(windows.WAIT_TIMEOUT) {
			continue
		}
		var n uint32
		err = windows.GetOverlappedResult(s.handle, &s.overlap, &n, false)
		if err != nil || n == 0 {
			w.emit(Event{At: time.Now().UTC(), Gap: true})
			if err != nil {
				return
			}
		} else {
			b := s.buffer[:n]
			offset := 0
			for offset+12 <= len(b) {
				next := int(binary.LittleEndian.Uint32(b[offset:]))
				action := binary.LittleEndian.Uint32(b[offset+4:])
				length := int(binary.LittleEndian.Uint32(b[offset+8:]))
				if length%2 != 0 || length < 0 || offset+12+length > len(b) {
					w.dropped.Store(true)
					break
				}
				chars := make([]uint16, length/2)
				for i := range chars {
					chars[i] = binary.LittleEndian.Uint16(b[offset+12+i*2:])
				}
				path := string(utf16.Decode(chars))
				w.emit(Event{Path: filepath.ToSlash(filepath.Join(s.prefix, path)), Action: action, At: time.Now().UTC()})
				if next == 0 {
					break
				}
				if next < 12 || offset+next > len(b) {
					w.dropped.Store(true)
					break
				}
				offset += next
			}
		}
		if err = s.arm(); err != nil {
			w.emit(Event{At: time.Now().UTC(), Gap: true})
			return
		}
	}
}
func (w *Watch) Dropped() bool { return w.dropped.Swap(false) }
func (w *Watch) Close()        { w.cancel(); w.wg.Wait() }
