package hostapi

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/remote/api"
	"github.com/brettmostert/agent-command/internal/remote/wire"
)

// Folders, for a client's folder chooser: paths are the host's, and errors
// say what's wrong there.

// maxRecent is how many recent folders folders.recent lists.
const maxRecent = 20

// resolve makes path absolute on the host: "~" and relative paths are from
// its home.
func (a *API) resolve(path string) string {
	path = strings.TrimSpace(path)
	switch {
	case path == "" || path == "~":
		return a.home
	case strings.HasPrefix(path, "~/"):
		return filepath.Join(a.home, path[2:])
	case !filepath.IsAbs(path):
		return filepath.Join(a.home, path)
	}
	return filepath.Clean(path)
}

// fsError is err as a client should see it, with path in its message.
func fsError(path string, err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return wire.Errorf(wire.CodeNotFound, "%s doesn't exist", path)
	case errors.Is(err, fs.ErrPermission):
		return wire.Errorf(api.CodePermission, "no permission to use %s", path)
	case errors.Is(err, fs.ErrExist):
		return wire.Errorf(api.CodeExists, "%s already exists", path)
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return wire.Errorf(wire.CodeInternal, "%s: %v", path, pe.Err)
	}
	return wire.Errorf(wire.CodeInternal, "%s: %v", path, err)
}

// folder is path resolved, as long as it's a folder.
func (a *API) folder(path string) (string, error) {
	dir := a.resolve(path)
	info, err := os.Stat(dir)
	if err != nil {
		return "", fsError(dir, err)
	}
	if !info.IsDir() {
		return "", wire.Errorf(api.CodeNotFolder, "%s isn't a folder", dir)
	}
	return dir, nil
}

func hidden(name string) bool { return strings.HasPrefix(name, ".") }

func (a *API) listFolders(_ context.Context, c *wire.Call) (any, error) {
	var p api.FolderPath
	if err := c.Decode(&p); err != nil {
		return nil, err
	}
	dir, err := a.folder(p.Path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fsError(dir, err)
	}
	out := api.FolderList{Path: dir, Home: a.home, Folders: []api.Folder{}}
	if parent := filepath.Dir(dir); parent != dir {
		out.Parent = parent
	}
	for _, e := range entries {
		name := e.Name()
		if hidden(name) && !p.Hidden {
			continue
		}
		full := filepath.Join(dir, name)
		isDir := e.IsDir()
		if e.Type()&fs.ModeSymlink != 0 { // a link to a folder is one
			info, err := os.Stat(full)
			isDir = err == nil && info.IsDir()
		}
		if isDir {
			out.Folders = append(out.Folders, api.Folder{Name: name, Path: full, Hidden: hidden(name)})
		}
	}
	slices.SortFunc(out.Folders, func(x, y api.Folder) int {
		if x.Hidden != y.Hidden {
			if x.Hidden {
				return 1
			}
			return -1
		}
		if c := strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name)); c != 0 {
			return c
		}
		return strings.Compare(x.Name, y.Name)
	})
	return out, nil
}

func (a *API) mkdir(_ context.Context, c *wire.Call) (any, error) {
	var p api.FolderPath
	if err := c.Decode(&p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Path) == "" {
		return nil, wire.Errorf(wire.CodeBadRequest, "no folder to make")
	}
	dir := a.resolve(p.Path)
	if err := os.Mkdir(dir, 0o750); err != nil {
		return nil, fsError(dir, err)
	}
	name := filepath.Base(dir)
	return api.Folder{Name: name, Path: dir, Hidden: hidden(name)}, nil
}

// recentFolders are the folders of the host's sessions, the most recently
// active first, leaving out ones that have gone.
func (a *API) recentFolders(ctx context.Context, _ *wire.Call) (any, error) {
	st, err := a.b.State(ctx)
	if err != nil {
		return nil, err
	}
	sessions := slices.Clone(st.Sessions)
	slices.SortStableFunc(sessions, func(x, y *core.Session) int { return lastActive(y).Compare(lastActive(x)) })
	out := api.FolderList{Home: a.home, Folders: []api.Folder{}}
	seen := map[string]bool{}
	for _, s := range sessions {
		dir := s.Cwd
		if dir == "" || seen[dir] || len(out.Folders) == maxRecent {
			continue
		}
		seen[dir] = true
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		name := filepath.Base(dir)
		out.Folders = append(out.Folders, api.Folder{Name: name, Path: dir, Hidden: hidden(name)})
	}
	return out, nil
}
