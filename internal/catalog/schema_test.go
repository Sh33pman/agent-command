package catalog

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite golden files")

const golden = "testdata/catalog.schema.json"

// The generated schema is committed, so a change to any op or wire type shows
// up in review, and client generators (3.1) have a file to read.
func TestSchemaMatchesGolden(t *testing.T) {
	got, err := Local.Schema(LocalProto)
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	if *update {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/catalog -update)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is out of date: run go test ./internal/catalog -update", golden)
	}
}

// schemaDoc is the generated document read back as plain JSON, as a client
// generator would see it.
type schemaDoc struct {
	Schema string                     `json:"$schema"`
	Proto  Range                      `json:"proto"`
	Ops    []map[string]any           `json:"ops"`
	Defs   map[string]json.RawMessage `json:"$defs"`
}

func readSchema(t *testing.T) schemaDoc {
	t.Helper()
	b, err := Local.Schema(LocalProto)
	if err != nil {
		t.Fatal(err)
	}
	var d schemaDoc
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatalf("the schema isn't JSON: %v", err)
	}
	return d
}

func TestSchemaDescribesEveryOp(t *testing.T) {
	d := readSchema(t)
	if d.Schema != SchemaDialect || d.Proto != LocalProto {
		t.Errorf("header: $schema %q, proto %v", d.Schema, d.Proto)
	}
	if len(d.Ops) != len(Local.Names()) {
		t.Fatalf("%d ops in the schema, %d in the catalog", len(d.Ops), len(Local.Names()))
	}
	for i, op := range d.Ops {
		o := Local.Ops()[i]
		if op["name"] != o.Name || op["scope"] != string(o.Scope) || op["kind"] != string(o.Kind) || op["grant"] != string(o.Grant) {
			t.Errorf("op %d: schema has %v, catalog has %+v", i, op, o)
		}
		tr, _ := op["transport"].(map[string]any)
		wantOp, wantKind := o.Transport()
		if tr["op"] != wantOp || (wantKind != "" && tr["kind"] != wantKind) {
			t.Errorf("%q: transport %v, want %s/%s", o.Name, tr, wantOp, wantKind)
		}
		if _, has := op["input"]; has != (o.Input != nil) {
			t.Errorf("%q: input in schema %v, in catalog %v", o.Name, has, o.Input != nil)
		}
		if _, has := op["output"]; has != (o.Output != nil) {
			t.Errorf("%q: output in schema %v, in catalog %v", o.Name, has, o.Output != nil)
		}
	}
}

// Every $ref points at a definition in the document, so a generator can
// resolve the whole thing from one file.
func TestSchemaRefsResolve(t *testing.T) {
	b, err := Local.Schema(LocalProto)
	if err != nil {
		t.Fatal(err)
	}
	d := readSchema(t)
	var walk func(v any)
	refs := 0
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if ref, ok := v["$ref"].(string); ok {
				refs++
				name := strings.TrimPrefix(ref, "#/$defs/")
				if _, ok := d.Defs[name]; !ok {
					t.Errorf("$ref %q has no definition", ref)
				}
			}
			for _, x := range v {
				walk(x)
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	var all any
	if err := json.Unmarshal(b, &all); err != nil {
		t.Fatal(err)
	}
	walk(all)
	if refs == 0 {
		t.Error("the schema has no $refs: types aren't being shared")
	}
}

func TestSchemaFollowsGoTypes(t *testing.T) {
	d := readSchema(t)
	var snap struct {
		Properties map[string]map[string]any `json:"properties"`
		Required   []string                  `json:"required"`
	}
	if err := json.Unmarshal(d.Defs["ipc.Snapshot"], &snap); err != nil {
		t.Fatalf("no ipc.Snapshot definition: %v", err)
	}
	if snap.Properties["sessions"]["type"] != "array" {
		t.Errorf("snapshot.sessions = %v, want an array", snap.Properties["sessions"])
	}
	// omitempty fields aren't required; the others are.
	req := strings.Join(snap.Required, ",")
	if !strings.Contains(req, "sessions") || strings.Contains(req, "inbox") {
		t.Errorf("snapshot required = %v: want sessions, not inbox", snap.Required)
	}

	var hook struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(d.Defs["ipc.HookEvent"], &hook); err != nil {
		t.Fatal(err)
	}
	// Raw JSON is any value, and times are RFC 3339 strings.
	if string(hook.Properties["payload"]) != "true" {
		t.Errorf("payload = %s, want any value (true)", hook.Properties["payload"])
	}
	if !strings.Contains(string(hook.Properties["at"]), `"date-time"`) {
		t.Errorf("at = %s, want a date-time string", hook.Properties["at"])
	}

	// Types are named with their package: ipc and api both have
	// RemoteHost-like names, and must not overwrite each other.
	for name := range d.Defs {
		if !strings.Contains(name, ".") {
			t.Errorf("definition %q isn't named with its package", name)
		}
	}
}

type clash struct{ A int }

func TestSchemaRefusesTwoTypesWithOneName(t *testing.T) {
	type wrapA struct{ T clash }
	c, err := New(
		Op{Name: "a", Description: "a", Scope: ScopeRead, Kind: KindRead, Grant: GrantHost, Output: wrapA{}},
		Op{Name: "b", Description: "b", Scope: ScopeRead, Kind: KindRead, Grant: GrantHost, Output: clashOther()},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Schema(Range{1, 1}); err == nil || !strings.Contains(err.Error(), "both named") {
		t.Errorf("Schema = %v, want a name clash", err)
	}
}

// clashOther returns a value of a different type also named catalog.clash.
func clashOther() any {
	type clash struct{ B time.Time }
	type wrapB struct{ T clash }
	return wrapB{}
}

func TestSchemaLeavesOutNothingTypes(t *testing.T) {
	c, err := New(Op{Name: "ping", Description: "p", Scope: ScopeRead, Kind: KindRead, Grant: GrantNone})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := c.Document(Range{1, 1})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Ops[0].Input != nil || doc.Ops[0].Output != nil || len(doc.Defs) != 0 {
		t.Errorf("an op with no input or output got schemas: %+v", doc.Ops[0])
	}
}
