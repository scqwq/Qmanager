package mediabackup

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"qqarchive/internal/media"
)

// Bound central-directory allocation before archive/zip constructs its File
// slice, including ZIP64 archives. Multi-volume archives are unsupported.
func checkZIPDirectory(reader io.ReaderAt, size int64) error {
	fail := errors.New("ZIP 中央目录无效、分卷或超过资源限制")
	if size < 22 || size > maxBytes+int64(maxManifest)*4 {
		return fail
	}
	length := size
	if length > 65557 {
		length = 65557
	}
	tail := make([]byte, int(length))
	if _, err := reader.ReadAt(tail, size-length); err != nil {
		return err
	}
	pos := bytes.LastIndex(tail, []byte{'P', 'K', 5, 6})
	if pos < 0 || pos+22 > len(tail) {
		return fail
	}
	eocd := tail[pos:]
	if int(binary.LittleEndian.Uint16(eocd[20:22]))+22 != len(eocd) {
		return fail
	}
	if binary.LittleEndian.Uint16(eocd[4:6]) != 0 || binary.LittleEndian.Uint16(eocd[6:8]) != 0 {
		return fail
	}
	count := uint64(binary.LittleEndian.Uint16(eocd[10:12]))
	directoryBytes := uint64(binary.LittleEndian.Uint32(eocd[12:16]))
	offset := uint64(binary.LittleEndian.Uint32(eocd[16:20]))
	if count == 65535 || directoryBytes == 0xffffffff || offset == 0xffffffff {
		absolute := size - length + int64(pos)
		if absolute < 20 {
			return fail
		}
		locator := make([]byte, 20)
		if _, err := reader.ReadAt(locator, absolute-20); err != nil {
			return err
		}
		if !bytes.Equal(locator[:4], []byte{'P', 'K', 6, 7}) || binary.LittleEndian.Uint32(locator[4:8]) != 0 || binary.LittleEndian.Uint32(locator[16:20]) != 1 {
			return fail
		}
		where := binary.LittleEndian.Uint64(locator[8:16])
		if where > uint64(absolute-76) {
			return fail
		}
		record := make([]byte, 56)
		if _, err := reader.ReadAt(record, int64(where)); err != nil {
			return err
		}
		if !bytes.Equal(record[:4], []byte{'P', 'K', 6, 6}) || binary.LittleEndian.Uint32(record[16:20]) != 0 || binary.LittleEndian.Uint32(record[20:24]) != 0 {
			return fail
		}
		count = binary.LittleEndian.Uint64(record[32:40])
		directoryBytes = binary.LittleEndian.Uint64(record[40:48])
		offset = binary.LittleEndian.Uint64(record[48:56])
	}
	if count > uint64(media.MaxFiles+maxDirectories+34) || directoryBytes > 128*1024*1024 || offset > uint64(size) || directoryBytes > uint64(size)-offset {
		return fail
	}
	return nil
}
