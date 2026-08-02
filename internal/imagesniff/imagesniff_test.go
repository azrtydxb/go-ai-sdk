package imagesniff

import "testing"

func TestSniffMediaType(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{
			name: "png",
			data: []byte("\x89PNG\r\n\x1a\nrest-of-file"),
			want: "image/png",
		},
		{
			name: "jpeg",
			data: []byte("\xFF\xD8\xFFrest-of-file"),
			want: "image/jpeg",
		},
		{
			name: "webp",
			data: []byte("RIFF\x00\x00\x00\x00WEBPrest-of-file"),
			want: "image/webp",
		},
		{
			name: "gif",
			data: []byte("GIF89arest-of-file"),
			want: "image/gif",
		},
		{
			name: "unknown falls back to png",
			data: []byte("not an image"),
			want: "image/png",
		},
		{
			name: "empty falls back to png",
			data: []byte{},
			want: "image/png",
		},
		{
			name: "too short for webp check falls back to png",
			data: []byte("RIFF"),
			want: "image/png",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SniffMediaType(tc.data); got != tc.want {
				t.Errorf("SniffMediaType(%q) = %q, want %q", tc.data, got, tc.want)
			}
		})
	}
}
