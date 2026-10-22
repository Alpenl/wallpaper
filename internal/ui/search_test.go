package ui

import (
	"fmt"
	"testing"
	"time"
	"wpapp/internal/app"
	"wpapp/internal/config"
)

func TestLibrarySearchMetadataAndColors(t *testing.T) {
	v := app.View{Library: []config.Item{
		{ID: "ocean", Title: "Ocean", Type: "video", Author: "Alice", Description: "Quiet morning", Tags: []string{"nature"}, Colors: []string{"blue", "蓝色"}},
		{ID: "forest", Title: "Forest", Type: "image", Colors: []string{"green", "绿色"}},
	}}
	s := &Settings{}
	for _, query := range []string{" ocean ", "ALICE", "quiet", "nature", "blue", "蓝色"} {
		s.query = query
		if got := s.filtered(v); len(got) != 1 || got[0].ID != "ocean" {
			t.Fatalf("query %q = %v", query, got)
		}
	}
	s.query = "blue"
	s.filter = 2
	if got := s.filtered(v); len(got) != 0 {
		t.Fatal("type filter must combine with color search", got)
	}
}
func BenchmarkLibrarySearch1000(b *testing.B) {
	v := app.View{Library: make([]config.Item, 1000)}
	for i := range v.Library {
		v.Library[i] = config.Item{ID: fmt.Sprint(i), Title: fmt.Sprintf("Wallpaper %d", i), Author: "Photographer", Tags: []string{"nature", "landscape"}, Colors: []string{"blue", "蓝色"}, AddedAt: time.Unix(int64(i), 0)}
	}
	s := &Settings{query: "blue"}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if len(s.filtered(v)) != 1000 {
			b.Fatal("missing results")
		}
	}
}
