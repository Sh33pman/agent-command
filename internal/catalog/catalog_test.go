package catalog

import (
	"slices"
	"strings"
	"testing"
)

func valid(name string) Op {
	return Op{Name: name, Description: "does a thing", Scope: ScopeRead, Kind: KindRead, Grant: GrantHost}
}

func TestNewRefusesBadOps(t *testing.T) {
	with := func(f func(*Op)) Op { o := valid("x"); f(&o); return o }
	cases := []struct {
		name string
		op   Op
		want string
	}{
		{"no name", with(func(o *Op) { o.Name = "" }), "no name"},
		{"mutate itself", with(func(o *Op) { o.Name = MutateOp }), "carrier"},
		{"no description", with(func(o *Op) { o.Description = "" }), "no description"},
		{"unknown scope", with(func(o *Op) { o.Scope = "root" }), `unknown scope "root"`},
		{"unknown kind", with(func(o *Op) { o.Kind = "" }), "unknown kind"},
		{"unknown grant", with(func(o *Op) { o.Grant = "some" }), `unknown grant "some"`},
		{"change with read scope", with(func(o *Op) { o.Kind = KindChange }), "needs more than the read scope"},
		{"destructive with read scope", with(func(o *Op) { o.Kind = KindDestructive }), "needs more than the read scope"},
		{"change ignoring the grant", with(func(o *Op) { o.Scope, o.Kind, o.Grant = ScopeAct, KindChange, GrantNone }), "only read ops can ignore"},
		{"runs code with read scope", with(func(o *Op) { o.RunsCode = true }), "runs code"},
		{"streaming mutation", with(func(o *Op) { o.Mutation, o.Stream = true, true }), "can't be a stream"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := New(tc.op)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("New() = %v, %v; want an error containing %q", c, err, tc.want)
			}
		})
	}
}

func TestNewReportsEveryProblemAtOnce(t *testing.T) {
	bad := valid("b")
	bad.Description = ""
	_, err := New(valid("a"), valid("a"), bad)
	if err == nil {
		t.Fatal("New accepted a duplicate and an op without a description")
	}
	for _, want := range []string{`op "a": registered twice`, `op "b": no description`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q doesn't mention %q", err, want)
		}
	}
}

func TestCatalogListsAndFindsOps(t *testing.T) {
	c, err := New(valid("session.rename"), valid("hello"), valid("group.create"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := c.Names(), []string{"group.create", "hello", "session.rename"}; !slices.Equal(got, want) {
		t.Errorf("Names() = %v, want %v (sorted)", got, want)
	}
	for _, name := range c.Names() {
		o, ok := c.Lookup(name)
		if !ok || o.Name != name {
			t.Errorf("Lookup(%q) = %v, %v", name, o.Name, ok)
		}
	}
	if _, ok := c.Lookup("nope"); ok {
		t.Error("Lookup found an op that isn't registered")
	}

	// Callers get a copy: changing it doesn't change the catalog.
	ops := c.Ops()
	ops[0].Scope = ScopeAdmin
	if o, _ := c.Lookup(ops[0].Name); o.Scope != ScopeRead {
		t.Error("changing Ops()'s result changed the catalog")
	}
}

func TestTransport(t *testing.T) {
	plain := valid("snapshot")
	if op, kind := plain.Transport(); op != "snapshot" || kind != "" {
		t.Errorf("plain op sent as %q/%q, want snapshot/\"\"", op, kind)
	}
	m := valid("session.rename")
	m.Mutation = true
	if op, kind := m.Transport(); op != MutateOp || kind != "session.rename" {
		t.Errorf("mutation sent as %q/%q, want mutate/session.rename", op, kind)
	}
}
