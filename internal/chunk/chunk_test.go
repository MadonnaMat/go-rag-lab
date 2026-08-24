package chunk

import (
	"strings"
	"testing"
)

func TestSplit(t *testing.T) {
	cases := []struct {
		name       string
		text       string
		size       int
		overlap    int
		wantChunks []string
	}{
		{
			name:       "empty text produces no chunks",
			text:       "",
			size:       10,
			overlap:    2,
			wantChunks: nil,
		},
		{
			name:       "text shorter than size is a single chunk",
			text:       "hello",
			size:       10,
			overlap:    2,
			wantChunks: []string{"hello"},
		},
		{
			name:       "text exactly size is a single chunk",
			text:       "0123456789",
			size:       10,
			overlap:    2,
			wantChunks: []string{"0123456789"},
		},
		{
			name:       "longer text overlaps between chunks",
			text:       "0123456789ABCDEF",
			size:       10,
			overlap:    4,
			wantChunks: []string{"0123456789", "6789ABCDEF"},
		},
		{
			// overlap 0 (not 2) so the second window starts exactly at
			// index 10, landing entirely in the trailing spaces — with a
			// nonzero overlap the window would re-capture "89" from the
			// first chunk and no longer be whitespace-only.
			name:       "whitespace-only trailing chunk is dropped",
			text:       "0123456789   ",
			size:       10,
			overlap:    0,
			wantChunks: []string{"0123456789"},
		},
		{
			name:       "multi-byte runes are not split mid-character",
			text:       strings.Repeat("é", 12), // 2 bytes each in UTF-8, 1 rune each
			size:       10,
			overlap:    2,
			wantChunks: []string{strings.Repeat("é", 10), strings.Repeat("é", 4)},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Split(tc.text, tc.size, tc.overlap)
			if err != nil {
				t.Fatalf("Split returned unexpected error: %v", err)
			}
			if len(got) != len(tc.wantChunks) {
				t.Fatalf("got %d chunks, want %d: %+v", len(got), len(tc.wantChunks), got)
			}
			for i, want := range tc.wantChunks {
				if got[i].Text != want {
					t.Errorf("chunk %d text = %q, want %q", i, got[i].Text, want)
				}
				if got[i].Index != i {
					t.Errorf("chunk %d Index = %d, want %d", i, got[i].Index, i)
				}
			}
		})
	}
}

func TestSplitErrors(t *testing.T) {
	cases := []struct {
		name    string
		size    int
		overlap int
	}{
		{name: "zero size", size: 0, overlap: 0},
		{name: "negative size", size: -1, overlap: 0},
		{name: "negative overlap", size: 10, overlap: -1},
		{name: "overlap equal to size", size: 10, overlap: 10},
		{name: "overlap greater than size", size: 10, overlap: 11},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Split("some text", tc.size, tc.overlap); err == nil {
				t.Fatalf("Split(size=%d, overlap=%d) = nil error, want an error", tc.size, tc.overlap)
			}
		})
	}
}
