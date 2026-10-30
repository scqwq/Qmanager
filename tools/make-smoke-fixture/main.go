// Developer-only synthetic media generator. Requires an explicit new target.
package main

import (
	"fmt"
	"os"
	"qqarchive/internal/testfixture"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./tools/make-smoke-fixture <new-directory>")
		os.Exit(2)
	}
	root := os.Args[1]
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "target must not exist")
		os.Exit(2)
	}
	for _, p := range []string{"Pic/2026-09/Ori/秋天.jpg", "Pic/2026-10/Ori/照片.png", "Pic/2026-10/Thumb/preview.png", "Video/2026-09/Thumb/cover.jpg", "Emoji/emoji-recv/2026-10/Ori/expression.jpg"} {
		if err := testfixture.Image(root, p); err != nil {
			panic(err)
		}
	}
	for p, b := range map[string][]byte{"Pic/2026-10/OriTemp/growing.tmp": []byte("partial"), "Pic/2026-10/Ori/empty.jpg": {}, "Ptt/2026-10/Ori/voice.amr": []byte("\x02#!SILK_V3synthetic")} {
		if err := testfixture.File(root, p, b); err != nil {
			panic(err)
		}
	}
	fmt.Println("created 8 synthetic files; no QQ data used")
}
