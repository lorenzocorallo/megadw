package mega

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// LinkKind identifies the two public-link forms supported by the downloader.
type LinkKind string

const (
	LinkKindFile   LinkKind = "file"
	LinkKindFolder LinkKind = "folder"
)

// PublicLink is the non-secret metadata extracted from a public MEGA URL.
// Key remains in memory only; callers must not persist the original URL.
type PublicLink struct {
	Kind         LinkKind
	Handle       string
	Key          string
	SelectedPath string
	SelectedNode string
}

// ParseLink parses modern and legacy public MEGA links without contacting MEGA.
//
// Supported forms are:
//   - https://mega.nz/file/<handle>#<key>
//   - https://mega.nz/folder/<handle>#<key>
//   - https://mega.nz/#F!<handle>!<key>
//   - https://mega.nz/#!<handle>!<key>
func ParseLink(rawURL string) (PublicLink, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return PublicLink{}, fmt.Errorf("%w: empty URL", ErrInvalidLink)
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return PublicLink{}, fmt.Errorf("%w: %v", ErrInvalidLink, err)
	}
	if parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" {
		return PublicLink{}, fmt.Errorf("%w: HTTPS MEGA links without userinfo or ports are required", ErrInvalidLink)
	}
	if !isMEGAHost(parsed.Hostname()) {
		return PublicLink{}, fmt.Errorf("%w: unsupported host %q", ErrInvalidLink, parsed.Host)
	}

	pathParts := make([]string, 0, 2)
	for _, part := range strings.Split(parsed.EscapedPath(), "/") {
		if part == "" {
			continue
		}
		decoded, decodeErr := url.PathUnescape(part)
		if decodeErr != nil {
			return PublicLink{}, fmt.Errorf("%w: invalid path escape", ErrInvalidLink)
		}
		pathParts = append(pathParts, decoded)
	}
	fragment := parsed.Fragment

	if len(pathParts) == 2 && (pathParts[0] == "file" || pathParts[0] == "folder") {
		kind := LinkKind(pathParts[0])
		key, tail, found := strings.Cut(fragment, "/")
		if !found {
			key = fragment
		}
		if err := validateLinkToken(pathParts[1], "handle"); err != nil {
			return PublicLink{}, err
		}
		if err := validateLinkToken(key, "key"); err != nil {
			return PublicLink{}, err
		}

		link := PublicLink{Kind: kind, Handle: pathParts[1], Key: key}
		if found && tail != "" {
			if kind != LinkKindFolder {
				return PublicLink{}, fmt.Errorf("%w: file links cannot select a folder path", ErrInvalidLink)
			}
			if strings.HasPrefix(tail, "file/") {
				link.SelectedNode = strings.TrimPrefix(tail, "file/")
				if err := validateLinkToken(link.SelectedNode, "selected node"); err != nil {
					return PublicLink{}, err
				}
			} else {
				link.SelectedPath = tail
			}
		}
		return link, nil
	}

	legacy := fragment
	if strings.HasPrefix(legacy, "F!") {
		return parseLegacyLink(LinkKindFolder, strings.TrimPrefix(legacy, "F!"))
	}
	if strings.HasPrefix(legacy, "!") {
		return parseLegacyLink(LinkKindFile, strings.TrimPrefix(legacy, "!"))
	}

	return PublicLink{}, fmt.Errorf("%w: unsupported path and fragment form", ErrInvalidLink)
}

func parseLegacyLink(kind LinkKind, value string) (PublicLink, error) {
	handle, key, found := strings.Cut(value, "!")
	if !found {
		return PublicLink{}, fmt.Errorf("%w: legacy link is missing a key", ErrInvalidLink)
	}
	if err := validateLinkToken(handle, "handle"); err != nil {
		return PublicLink{}, err
	}
	if err := validateLinkToken(key, "key"); err != nil {
		return PublicLink{}, err
	}
	return PublicLink{Kind: kind, Handle: handle, Key: key}, nil
}

func isMEGAHost(host string) bool {
	switch strings.ToLower(host) {
	case "mega.nz", "www.mega.nz", "mega.co.nz", "www.mega.co.nz":
		return true
	default:
		return false
	}
}

func validateLinkToken(value, label string) error {
	if value == "" || len(value) > 1024 {
		return fmt.Errorf("%w: missing %s", ErrInvalidLink, label)
	}
	for _, r := range value {
		if r > unicode.MaxASCII || !(r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') {
			return fmt.Errorf("%w: invalid %s", ErrInvalidLink, label)
		}
	}
	return nil
}
