package catalog

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/ipc"
)

// ipcConsts reads internal/ipc's source for its string constants named with
// prefix, so an op added there and not here fails this test.
func ipcConsts(t *testing.T, prefix string) map[string]string {
	t.Helper()
	files, err := filepath.Glob("../ipc/*.go")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, name := range vs.Names {
					if !strings.HasPrefix(name.Name, prefix) || i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					v, _ := strconv.Unquote(lit.Value)
					out[name.Name] = v
				}
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("found no %s constants in internal/ipc", prefix)
	}
	return out
}

func TestLocalRegistersEveryIPCOp(t *testing.T) {
	known := map[string]bool{}
	for name, op := range ipcConsts(t, "Op") {
		if op == ipc.OpMutate {
			continue // the carrier, not an op
		}
		known[op] = true
		o, ok := Local.Lookup(op)
		switch {
		case !ok:
			t.Errorf("ipc.%s (%q) isn't in the catalog", name, op)
		case o.Mutation:
			t.Errorf("ipc.%s (%q) is a top-level op, but the catalog sends it through mutate", name, op)
		}
	}
	for name, kind := range ipcConsts(t, "Mut") {
		known[kind] = true
		o, ok := Local.Lookup(kind)
		switch {
		case !ok:
			t.Errorf("ipc.%s (%q) isn't in the catalog", name, kind)
		case !o.Mutation:
			t.Errorf("ipc.%s (%q) is a mutation kind, but the catalog sends it as its own op", name, kind)
		}
	}
	for _, name := range Local.Names() {
		if !known[name] {
			t.Errorf("the catalog has %q, which internal/ipc doesn't define", name)
		}
	}
}

func TestLocalOpsDeclareTheirLimits(t *testing.T) {
	for _, o := range Local.Ops() {
		if o.Description == "" || o.Scope == "" || o.Kind == "" || o.Grant == "" {
			t.Errorf("%q: missing description, scope, kind or grant", o.Name)
		}
		if o.Mutation {
			if _, ok := o.Input.(ipc.Mutation); !ok {
				t.Errorf("%q: sent through mutate, but its input isn't ipc.Mutation", o.Name)
			}
		}
	}
}

// The limits that keep remote callers, agents and shares from doing harm.
func TestLocalPolicy(t *testing.T) {
	get := func(name string) Op {
		t.Helper()
		o, ok := Local.Lookup(name)
		if !ok {
			t.Fatalf("%q isn't registered", name)
		}
		return o
	}

	// Only the handshake ignores the caller's grant.
	for _, o := range Local.Ops() {
		if o.Grant == GrantNone && o.Name != ipc.OpHello && o.Name != ipc.OpPing {
			t.Errorf("%q ignores the caller's grant", o.Name)
		}
	}

	// Deleting for good, sharing and who's allowed in need admin.
	for _, name := range []string{ipc.MutPurge, ipc.MutRemote, ipc.OpRemoteApprove, ipc.OpRemoteAuthorize,
		ipc.OpRemoteRevoke, ipc.OpRemoteHosting, ipc.OpWakeSet, ipc.OpShutdown} {
		if o := get(name); o.Scope != ScopeAdmin {
			t.Errorf("%q has scope %s, want admin", name, o.Scope)
		}
	}
	if o := get(ipc.MutPurge); o.Kind != KindDestructive {
		t.Errorf("purge is %s, want destructive", o.Kind)
	}

	// Seeing who's authorized is admin, even though it changes nothing.
	for _, name := range []string{ipc.OpRemoteClients, ipc.OpRemoteRequests} {
		if o := get(name); o.Scope != ScopeAdmin || o.Kind != KindRead {
			t.Errorf("%q is %s/%s, want admin/read", name, o.Scope, o.Kind)
		}
	}

	// Hooks, stopping the daemon and the admin password only work on the host.
	for _, name := range []string{ipc.OpEvent, ipc.OpShutdown, ipc.OpWakeSet} {
		if !get(name).HostOnly {
			t.Errorf("%q isn't host-only", name)
		}
	}

	// Ops that start agents or type into sessions say so.
	for _, name := range []string{ipc.OpNew, ipc.OpResume, ipc.OpInboxAnswer, ipc.OpJobSave, ipc.OpJobRun, ipc.OpRemoteNew} {
		if !get(name).RunsCode {
			t.Errorf("%q doesn't say it runs code", name)
		}
	}

	// Jobs run agents on the host: never through a share.
	for _, o := range Local.Ops() {
		if (strings.HasPrefix(o.Name, "job.") || strings.HasPrefix(o.Name, "harness")) && o.Grant != GrantHost {
			t.Errorf("%q has grant %s, want host", o.Name, o.Grant)
		}
	}

	// Sessions, groups and the Inbox are filtered to what's granted.
	for _, o := range Local.Ops() {
		if strings.HasPrefix(o.Name, "session.") || strings.HasPrefix(o.Name, "group.") || strings.HasPrefix(o.Name, "inbox") {
			if o.Grant != GrantEntities {
				t.Errorf("%q has grant %s, want entities", o.Name, o.Grant)
			}
		}
	}
}

func TestLocalProtoIsTheSocketsVersion(t *testing.T) {
	if LocalProto != (Range{Min: ipc.Proto, Max: ipc.Proto}) || !LocalProto.Valid() {
		t.Errorf("LocalProto = %v, want %d", LocalProto, ipc.Proto)
	}
}

func TestMustNewPanicsOnABadOp(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("mustNew accepted an op without a name")
		}
	}()
	mustNew(Op{})
}
