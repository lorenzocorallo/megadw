package mega

import (
	"errors"
	"testing"
)

func TestParseModernPublicLinks(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want PublicLink
	}{
		{
			name: "file",
			raw:  "https://mega.nz/file/file0001#ABEiM0RVZneImaq7zN3u_xAgMEBQYHCAkKCwwNDg8AA",
			want: PublicLink{Kind: LinkKindFile, Handle: "file0001", Key: "ABEiM0RVZneImaq7zN3u_xAgMEBQYHCAkKCwwNDg8AA"},
		},
		{
			name: "folder with selected path",
			raw:  "https://www.mega.nz/folder/folder01#AQIDBAUGBwgJCgsMDQ4PEA/file/nested01",
			want: PublicLink{
				Kind:         LinkKindFolder,
				Handle:       "folder01",
				Key:          "AQIDBAUGBwgJCgsMDQ4PEA",
				SelectedNode: "nested01",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseLink(test.raw)
			if err != nil {
				t.Fatalf("ParseLink() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("ParseLink() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestParseLegacyPublicLinks(t *testing.T) {
	file, err := ParseLink("https://mega.nz/#!file0001!ABEiM0RVZneImaq7zN3u_xAgMEBQYHCAkKCwwNDg8AA")
	if err != nil {
		t.Fatalf("legacy file error = %v", err)
	}
	if file.Kind != LinkKindFile || file.Handle != "file0001" {
		t.Fatalf("legacy file = %#v", file)
	}

	folder, err := ParseLink("https://mega.co.nz/#F!folder01!AQIDBAUGBwgJCgsMDQ4PEA")
	if err != nil {
		t.Fatalf("legacy folder error = %v", err)
	}
	if folder.Kind != LinkKindFolder || folder.Handle != "folder01" {
		t.Fatalf("legacy folder = %#v", folder)
	}
}

func TestParseLinkRejectsUnsafeOrUnsupportedURLs(t *testing.T) {
	for _, raw := range []string{
		"http://mega.nz/file/file0001#key",
		"https://example.test/file/file0001#key",
		"https://mega.nz.evil/file/file0001#key",
		"https://user:pass@mega.nz/file/file0001#key",
		"https://mega.nz:443/file/file0001#key",
		"https://mega.nz/file/file0001#bad!key",
		"https://mega.nz/file/file0001#key/file/other",
		"https://mega.nz/file/file0001#",
	} {
		_, err := ParseLink(raw)
		if !errors.Is(err, ErrInvalidLink) {
			t.Errorf("ParseLink(%q) error = %v, want ErrInvalidLink", raw, err)
		}
	}
}
