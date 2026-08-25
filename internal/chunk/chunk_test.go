package chunk

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			require.NoError(t, err)
			require.Len(t, got, len(tc.wantChunks))
			for i, want := range tc.wantChunks {
				assert.Equal(t, want, got[i].Text, "chunk %d text", i)
				assert.Equal(t, i, got[i].Index, "chunk %d Index", i)
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
			_, err := Split("some text", tc.size, tc.overlap)
			require.Error(t, err)
		})
	}
}
