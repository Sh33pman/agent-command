package daemon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/api"
	"github.com/brettmostert/agent-command/internal/remote/hostapi"
)

// A job's prompt file, read and written by clients wherever they run: only a
// saved job's own file, by its id, or for a job not saved yet, a Markdown or
// text file at the draft's folder and path. Nothing else.

// promptFile is the file ref names, and whether it's a saved job's.
func (d *Daemon) promptFile(ctx context.Context, ref ipc.PromptFileRef) (string, error) {
	if ref.JobID != "" {
		d.mu.Lock()
		j, err := d.store.Job(ctx, ref.JobID)
		d.mu.Unlock()
		if err != nil {
			return "", err
		}
		if j.PromptFile == "" {
			return "", invalid("%s has no prompt file: its prompt is in the job", j.Name)
		}
		return promptPath(j), nil
	}
	if ref.Path == "" {
		return "", invalid("which prompt file? Give the job, or the draft's file")
	}
	path := promptPath(&core.Job{JobSpec: core.JobSpec{PromptFile: ref.Path, Cwd: expandHome(ref.Cwd)}})
	if !filepath.IsAbs(path) {
		return "", invalid("%s is relative: choose the job's folder first", ref.Path)
	}
	if !hostapi.IsPromptFile(path) {
		return "", invalid("a prompt file is Markdown or text (.md, .markdown, .txt), not %s", filepath.Base(path))
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return "", invalid("%s isn't a plain file", path)
	}
	return path, nil
}

func (d *Daemon) readPromptFile(ctx context.Context, ref ipc.PromptFileRef) (ipc.PromptFile, error) {
	path, err := d.promptFile(ctx, ref)
	if err != nil {
		return ipc.PromptFile{}, err
	}
	out := ipc.PromptFile{Path: path}
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return out, nil // not there yet: writing makes it
	case err != nil:
		return out, err
	case info.Size() > maxPromptFile:
		return out, invalid("%s is over 1 MB", path)
	}
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return out, err
	}
	out.Text, out.Exists = string(b), true
	return out, nil
}

func (d *Daemon) writePromptFile(ctx context.Context, ref ipc.PromptFileRef) (ipc.PromptFile, error) {
	path, err := d.promptFile(ctx, ref)
	if err != nil {
		return ipc.PromptFile{}, err
	}
	if len(ref.Text) > maxPromptFile {
		return ipc.PromptFile{}, invalid("the prompt is over 1 MB")
	}
	if err := os.WriteFile(path, []byte(ref.Text), 0o600); err != nil {
		return ipc.PromptFile{}, fmt.Errorf("couldn't save %s: %w", path, err)
	}
	return ipc.PromptFile{Path: path, Text: ref.Text, Exists: true}, nil
}

// This machine's folders, for every client's chooser.

func (d *Daemon) home() string {
	if d.cfg.Home != "" {
		return d.cfg.Home
	}
	home, _ := os.UserHomeDir()
	return home
}

func isFolderOp(op string) bool {
	switch op {
	case ipc.OpFoldersRecent, ipc.OpFoldersList, ipc.OpFoldersMkdir, ipc.OpJobPromptRead, ipc.OpJobPromptWrite:
		return true
	}
	return false
}

func (d *Daemon) handleFolders(ctx context.Context, req ipc.Request) (any, error) {
	switch req.Op {
	case ipc.OpFoldersRecent:
		d.mu.Lock()
		sessions, err := d.store.Sessions(ctx, false)
		d.mu.Unlock()
		if err != nil {
			return nil, err
		}
		local := sessions[:0]
		for _, s := range sessions {
			if !s.OnHost() {
				local = append(local, s)
			}
		}
		return hostapi.RecentFolders(d.home(), local), nil
	case ipc.OpFoldersList:
		p, err := decode[api.FolderPath](req.Data)
		if err != nil {
			return nil, err
		}
		return hostapi.ListFolders(d.home(), p)
	case ipc.OpFoldersMkdir:
		p, err := decode[api.FolderPath](req.Data)
		if err != nil {
			return nil, err
		}
		return hostapi.MakeFolder(d.home(), p.Path)
	case ipc.OpJobPromptRead, ipc.OpJobPromptWrite:
		ref, err := decode[ipc.PromptFileRef](req.Data)
		if err != nil {
			return nil, err
		}
		if req.Op == ipc.OpJobPromptRead {
			return d.readPromptFile(ctx, ref)
		}
		return d.writePromptFile(ctx, ref)
	}
	return nil, fmt.Errorf("unknown op %q", req.Op)
}
