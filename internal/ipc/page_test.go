package ipc

import "testing"

func TestPages(t *testing.T) {
	for limit, want := range map[int]int{0: PageSize, 7: 7, PageMax + 1: PageMax} {
		if got, err := (Page{Limit: limit}).Size(); err != nil || got != want {
			t.Errorf("limit %d: %d %v", limit, got, err)
		}
	}
	if _, err := (Page{Limit: -1}).Size(); err == nil {
		t.Error("a negative limit")
	}
	if n, err := (Page{Cursor: BeforeCursor(42)}).Before(); err != nil || n != 42 {
		t.Errorf("before = %d %v", n, err)
	}
	if n, err := (Page{Cursor: OffsetCursor(3)}).Offset(); err != nil || n != 3 {
		t.Errorf("offset = %d %v", n, err)
	}
	for _, bad := range []string{"nope", OffsetCursor(3), "Yg", encodeCursor(cursorBefore, -1)} {
		if _, err := (Page{Cursor: bad}).Before(); err == nil {
			t.Errorf("cursor %q isn't a before one", bad)
		}
	}

	list := []int{1, 2, 3, 4, 5}
	got, next, err := PageOf(list, Page{Limit: 2})
	if err != nil || len(got) != 2 || got[0] != 1 || next == "" {
		t.Fatalf("first = %v %q %v", got, next, err)
	}
	var all []int
	for p := (Page{Limit: 2}); ; {
		page, next, err := PageOf(list, p)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, page...)
		if next == "" {
			break
		}
		p.Cursor = next
	}
	if len(all) != 5 || all[4] != 5 {
		t.Errorf("every page = %v", all)
	}
	if page, next, err := PageOf(list, Page{Cursor: OffsetCursor(9)}); err != nil || len(page) != 0 || next != "" {
		t.Errorf("past the end = %v %q %v", page, next, err)
	}
	if _, _, err := PageOf(list, Page{Limit: -2}); err == nil {
		t.Error("a negative limit")
	}
	if _, _, err := PageOf(list, Page{Cursor: BeforeCursor(1)}); err == nil {
		t.Error("another list's cursor")
	}
}
