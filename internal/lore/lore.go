// Package lore holds small helpers shared by the code that reads .md files
// out of LORE_DIR — the chat get_resource tool and internal/api's /lore
// endpoint — so the filename-safety rule lives in exactly one place.
package lore

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// SafeName validates a caller-supplied lore filename: it must be a bare
// ".md" base name with no directory component, so a reader can only ever
// touch files directly inside LORE_DIR. It returns the cleaned name.
func SafeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("missing required argument \"name\"")
	}
	if name != filepath.Base(name) || strings.Contains(name, "/") || strings.Contains(name, `\`) {
		return "", fmt.Errorf("invalid resource name %q: must be a bare filename", name)
	}
	if filepath.Ext(name) != ".md" {
		return "", fmt.Errorf("invalid resource name %q: must end in .md", name)
	}
	return name, nil
}
