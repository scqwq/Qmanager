package media

import "testing"

func TestVideoCoverMatchingRejectsAmbiguityAndDifferentMonths(t *testing.T) {
	v := Item{ID: "video", RelativePath: "Video/2026-09/Ori/0123456789abcdef0123456789abcdef.mp4", SourceMonth: "2026-09", Kind: "video", Role: "original", Format: "iso-bmff", Status: "recognized"}
	c := Item{ID: "cover", RelativePath: "Video/2026-09/Thumb/0123456789abcdef0123456789abcdef_0.png", SourceMonth: v.SourceMonth, Kind: "video", Role: "cover", Format: "jpeg", Status: "recognized", Preview: true, Bytes: 100}
	if got := MatchVideoCovers([]Item{v}, []Item{c})[v.ID]; got.ID != c.ID {
		t.Fatal("missing unique cover")
	}
	for _, change := range []func(*Item){
		func(i *Item) {
			i.RelativePath = "Video/2026-08/Thumb/0123456789abcdef0123456789abcdef_0.png"
			i.SourceMonth = "2026-08"
		},
		func(i *Item) { i.SourceMonth = "2026-08" },
		func(i *Item) { i.Role = "temporary" },
		func(i *Item) { i.Preview = false },
		func(i *Item) { i.Status = "changed" },
	} {
		bad := c
		change(&bad)
		if len(MatchVideoCovers([]Item{v}, []Item{bad})) != 0 {
			t.Fatal("unsafe candidate matched", bad)
		}
	}
	if len(MatchVideoCovers([]Item{v}, []Item{c, c})) != 0 {
		t.Fatal("ambiguous cover matched")
	}
	v.RelativePath = "Video/2026-09/Ori/unrelated.mp4"
	if VideoCoverPath(v) != "" {
		t.Fatal("unknown naming convention guessed")
	}
}
