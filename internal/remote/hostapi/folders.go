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
func (a *API) resolve(path string) string { return Resolve(a.home, path) }

// Resolve makes path absolute: "~" and relative paths are from home.
func Resolve(home, path string) string {
	path = strings.TrimSpace(path)
	switch {
	case path == "" || path == "~":
		return home
	case strings.HasPrefix(path, "~/"):
		return filepath.Join(home, path[2:])
	case !filepath.IsAbs(path):
		return filepath.Join(home, path)
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
func (a *API) folder(path string) (string, error) { return folderAt(a.home, path) }

func folderAt(home, path string) (string, error) {
	dir := Resolve(home, path)
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
	return ListFolders(a.home, p)
}

// PromptExts are the files a folder listing offers with Files: Markdown and
// plain text, what a prompt file can be.
var PromptExts = []string{".md", ".markdown", ".txt"}

// IsPromptFile says whether name is one a prompt file can be.
func IsPromptFile(name string) bool {
	return slices.Contains(PromptExts, strings.ToLower(filepath.Ext(name)))
}

// hasExt says whether a file is one a listing offers: one of exts, or with
// none, a prompt file.
func hasExt(name string, exts []string) bool {
	if len(exts) == 0 {
		return IsPromptFile(name)
	}
	return slices.ContainsFunc(exts, func(e string) bool { return strings.EqualFold(filepath.Ext(name), e) })
}

// ListFolders lists a folder's subfolders, and with p.Files its Markdown and
// text files, for a chooser. home is where "~" starts.
func ListFolders(home string, p api.FolderPath) (api.FolderList, error) {
	dir, err := folderAt(home, p.Path)
	if err != nil {
		return api.FolderList{}, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return api.FolderList{}, fsError(dir, err)
	}
	out := api.FolderList{Path: dir, Home: home, Folders: []api.Folder{}}
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
		switch {
		case isDir:
			out.Folders = append(out.Folders, api.Folder{Name: name, Path: full, Hidden: hidden(name)})
		case p.Files && e.Type().IsRegular() && hasExt(name, p.Exts):
			out.Files = append(out.Files, api.Folder{Name: name, Path: full, Hidden: hidden(name)})
		}
	}
	byName := func(x, y api.Folder) int {
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
	}
	slices.SortFunc(out.Folders, byName)
	slices.SortFunc(out.Files, byName)
	return out, nil
}

func (a *API) mkdir(_ context.Context, c *wire.Call) (any, error) {
	var p api.FolderPath
	if err := c.Decode(&p); err != nil {
		return nil, err
	}
	return MakeFolder(a.home, p.Path)
}

// MakeFolder makes a folder, from home when path is relative.
func MakeFolder(home, path string) (api.Folder, error) {
	if strings.TrimSpace(path) == "" {
		return api.Folder{}, wire.Errorf(wire.CodeBadRequest, "no folder to make")
	}
	dir := Resolve(home, path)
	if err := os.Mkdir(dir, 0o750); err != nil {
		return api.Folder{}, fsError(dir, err)
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
	return RecentFolders(a.home, st.Sessions), nil
}

// RecentFolders are the folders of these sessions, the most recently active
// first, leaving out ones that have gone.
func RecentFolders(home string, all []*core.Session) api.FolderList {
	sessions := slices.Clone(all)
	slices.SortStableFunc(sessions, func(x, y *core.Session) int { return lastActive(y).Compare(lastActive(x)) })
	out := api.FolderList{Home: home, Folders: []api.Folder{}}
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
	return out
}
