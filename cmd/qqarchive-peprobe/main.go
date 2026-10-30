// qqarchive-peprobe only reads a PE file. It never opens or alters a process.
package main

import (
	"bytes"
	"crypto/sha256"
	"debug/pe"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
)

type candidate struct {
	Reference string `json:"reference_rva"`
	Begin     string `json:"candidate_function_begin_rva"`
	End       string `json:"candidate_function_end_rva"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: qqarchive-peprobe wrapper.node (static analysis only)")
		os.Exit(1)
	}
	if err := probe(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func probe(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 256<<20 {
		return fmt.Errorf("unsupported file size or type")
	}
	data := make([]byte, info.Size())
	if _, err = file.ReadAt(data, 0); err != nil {
		return err
	}
	hash := sha256.Sum256(data)
	p, err := pe.NewFile(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer p.Close()
	h, ok := p.OptionalHeader.(*pe.OptionalHeader64)
	if !ok || p.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		return fmt.Errorf("requires AMD64 PE image")
	}
	marker := []byte("nt_sqlite3_key_v2: db=%p zDb=%s\x00")
	var targets []uint32
	for _, s := range p.Sections {
		if s.Characteristics&pe.IMAGE_SCN_MEM_EXECUTE != 0 {
			continue
		}
		b, e := s.Data()
		if e != nil {
			return e
		}
		for offset := 0; offset < len(b); {
			i := bytes.Index(b[offset:], marker)
			if i < 0 {
				break
			}
			i += offset
			targets = append(targets, s.VirtualAddress+uint32(i))
			offset = i + len(marker)
		}
	}
	dd := h.DataDirectory[3]
	var unwind []byte
	for _, s := range p.Sections {
		if uint64(dd.VirtualAddress) >= uint64(s.VirtualAddress) && uint64(dd.VirtualAddress)+uint64(dd.Size) <= uint64(s.VirtualAddress)+uint64(s.Size) {
			b, e := s.Data()
			if e != nil {
				return e
			}
			off := dd.VirtualAddress - s.VirtualAddress
			unwind = b[off : off+dd.Size]
			break
		}
	}
	if len(unwind) == 0 || len(unwind)%12 != 0 {
		return fmt.Errorf("missing or invalid AMD64 unwind table")
	}
	results := []candidate{}
	for _, s := range p.Sections {
		if s.Characteristics&pe.IMAGE_SCN_MEM_EXECUTE == 0 {
			continue
		}
		b, e := s.Data()
		if e != nil {
			return e
		}
		for i := 0; i+7 <= len(b); i++ {
			// Strict candidate encoding: LEA RDX,[RIP+disp32]. This byte scan
			// is not a disassembler; a human must verify instruction boundaries.
			if b[i] != 0x48 || b[i+1] != 0x8d || b[i+2] != 0x15 {
				continue
			}
			rva := s.VirtualAddress + uint32(i)
			dst := int64(rva) + 7 + int64(int32(binary.LittleEndian.Uint32(b[i+3:i+7])))
			match := false
			for _, target := range targets {
				if dst == int64(target) {
					match = true
				}
			}
			if !match {
				continue
			}
			for j := 0; j+12 <= len(unwind); j += 12 {
				begin, end := binary.LittleEndian.Uint32(unwind[j:j+4]), binary.LittleEndian.Uint32(unwind[j+4:j+8])
				if begin <= rva && rva < end {
					results = append(results, candidate{fmt.Sprintf("0x%X", rva), fmt.Sprintf("0x%X", begin), fmt.Sprintf("0x%X", end)})
				}
			}
		}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"status": "STATIC_CANDIDATES_ONLY", "sha256": hex.EncodeToString(hash[:]), "marker_count": len(targets), "candidates": results, "key_extracted": false, "process_access": false})
}
