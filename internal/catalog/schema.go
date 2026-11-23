package catalog

import (
	"encoding/json"
	"fmt"
	"path"
	"reflect"

	"github.com/invopop/jsonschema"

	"github.com/brettmostert/agent-command/internal/errcode"
)

// SchemaDialect is the JSON Schema version Schema writes.
const SchemaDialect = "https://json-schema.org/draft/2020-12/schema"

// Document is the catalog as JSON: every op, and the schemas of its input
// and output types under $defs. Generated client types and tools are made
// from it.
type Document struct {
	Schema string `json:"$schema"`
	Title  string `json:"title"`
	// Proto is the range of protocol versions this catalog's daemon speaks.
	Proto Range   `json:"proto"`
	Ops   []OpDoc `json:"ops"`
	// Errors are the codes a failed op answers with, on every transport.
	Errors []errcode.Code                `json:"errors"`
	Defs   map[string]*jsonschema.Schema `json:"$defs"`
}

// OpDoc is one op in the Document. Input and Output are schemas, usually a
// $ref into Document.Defs; they're left out when the op takes or returns
// nothing.
type OpDoc struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Scope       Scope  `json:"scope"`
	Kind        Kind   `json:"kind"`
	Grant       Grant  `json:"grant"`
	HostOnly    bool   `json:"host_only,omitempty"`
	// NeedsSessions: a client-only daemon answers not_supported.
	NeedsSessions bool               `json:"needs_sessions,omitempty"`
	RunsCode      bool               `json:"runs_code,omitempty"`
	Stream        bool               `json:"stream,omitempty"`
	Retryable     bool               `json:"retryable,omitempty"`
	Transport     Transport          `json:"transport"`
	Input         *jsonschema.Schema `json:"input,omitempty"`
	Output        *jsonschema.Schema `json:"output,omitempty"`
}

// Transport is how protocol 1 sends an op.
type Transport struct {
	Op   string `json:"op"`
	Kind string `json:"kind,omitempty"`
}

// Document builds the catalog's Document for a daemon speaking proto.
func (c *Catalog) Document(proto Range) (*Document, error) {
	doc := &Document{
		Schema: SchemaDialect,
		Title:  "acmd daemon ops",
		Proto:  proto,
		Errors: errcode.All,
		Defs:   map[string]*jsonschema.Schema{},
	}
	r := reflector()
	for _, o := range c.ops {
		op, kind := o.Transport()
		d := OpDoc{
			Name: o.Name, Description: o.Description,
			Scope: o.Scope, Kind: o.Kind, Grant: o.Grant,
			HostOnly: o.HostOnly, NeedsSessions: o.NeedsSessions, RunsCode: o.RunsCode, Stream: o.Stream, Retryable: o.Retryable,
			Transport: Transport{Op: op, Kind: kind},
		}
		var err error
		if d.Input, err = typeSchema(r, o.Input, doc.Defs); err != nil {
			return nil, fmt.Errorf("op %q input: %w", o.Name, err)
		}
		if d.Output, err = typeSchema(r, o.Output, doc.Defs); err != nil {
			return nil, fmt.Errorf("op %q output: %w", o.Name, err)
		}
		doc.Ops = append(doc.Ops, d)
	}
	return doc, nil
}

// Schema is the Document as indented JSON.
func (c *Catalog) Schema(proto Range) ([]byte, error) {
	doc, err := c.Document(proto)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(doc, "", "  ")
}

var rawMessage = reflect.TypeFor[json.RawMessage]()

func reflector() *jsonschema.Reflector {
	return &jsonschema.Reflector{
		// Types are named with their package, since ipc, api and config
		// share type names (RemoteHost, Session, …).
		Namer: typeName,
		// Raw JSON is any value, not bytes.
		Mapper: func(t reflect.Type) *jsonschema.Schema {
			if t == rawMessage {
				return &jsonschema.Schema{}
			}
			return nil
		},
		// Clients must accept fields they don't know (8.6).
		AllowAdditionalProperties: true,
	}
}

// typeName is "pkg.Name" for a named type of a package, and the plain name
// otherwise (builtins, unnamed types).
func typeName(t reflect.Type) string {
	if t.PkgPath() == "" || t.Name() == "" {
		return t.Name()
	}
	return path.Base(t.PkgPath()) + "." + t.Name()
}

// typeSchema returns v's schema, adding the types it names to defs. Two
// different types under one name is an error, never a silent overwrite.
func typeSchema(r *jsonschema.Reflector, v any, defs map[string]*jsonschema.Schema) (*jsonschema.Schema, error) {
	if v == nil {
		return nil, nil
	}
	s := r.Reflect(v)
	for name, def := range s.Definitions {
		if old, ok := defs[name]; ok {
			a, _ := json.Marshal(old)
			b, _ := json.Marshal(def)
			if string(a) != string(b) {
				return nil, fmt.Errorf("two types are both named %q", name)
			}
			continue
		}
		defs[name] = def
	}
	s.Definitions = nil
	s.Version = ""
	s.ID = ""
	return s, nil
}
