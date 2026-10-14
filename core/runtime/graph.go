package runtime

import (
	"fmt"
	"strings"

	"github.com/cuihairu/chame/core/capability"
	"github.com/cuihairu/chame/core/module"
)

// node is one registered module with its metadata.
type node struct {
	mod   module.Module
	info  module.Info
	order int
}

// graph is the module dependency graph (spec §4.4).
type graph struct {
	byName map[string]*node
	order  []*node // registration order
}

func newGraph() *graph {
	return &graph{byName: make(map[string]*node)}
}

func (g *graph) add(mod module.Module, info module.Info, order int) error {
	name := mod.Name()
	if name == "" {
		return fmt.Errorf("runtime: module name must not be empty")
	}
	if _, dup := g.byName[name]; dup {
		return fmt.Errorf("runtime: module %q registered twice", name)
	}
	if err := info.Validate(name); err != nil {
		return err
	}
	n := &node{mod: mod, info: info, order: order}
	g.byName[name] = n
	g.order = append(g.order, n)
	return nil
}

// resolve validates the graph and returns the start order (spec §4.4
// steps 1-6). The reverse of the result is the stop order.
func (g *graph) resolve() ([]*node, error) {
	if err := g.checkMissing(); err != nil {
		return nil, err
	}
	if err := g.checkCapabilities(); err != nil {
		return nil, err
	}
	return g.startOrder()
}

// checkMissing rejects required dependencies that are absent.
func (g *graph) checkMissing() error {
	for _, n := range g.order {
		for _, d := range n.info.Dependencies {
			if d.Optional {
				continue
			}
			if _, ok := g.byName[d.Name]; !ok {
				return fmt.Errorf("runtime: module %q requires missing module %q", n.mod.Name(), d.Name)
			}
		}
	}
	return nil
}

// checkCapabilities enforces that every required capability is provided
// by some module, and that no capability has two providers (first-release
// policy, spec §4.4 step 3).
func (g *graph) checkCapabilities() error {
	providers := make(map[capability.Name]string)
	for _, n := range g.order {
		for _, c := range n.info.Provides {
			if prev, dup := providers[c]; dup {
				return fmt.Errorf("runtime: capability %q provided by both %q and %q", c, prev, n.mod.Name())
			}
			providers[c] = n.mod.Name()
		}
	}
	for _, n := range g.order {
		for _, c := range n.info.Requires {
			if _, ok := providers[c]; !ok {
				return fmt.Errorf("runtime: module %q requires capability %q that no module provides", n.mod.Name(), c)
			}
		}
	}
	return nil
}

// startOrder computes a deterministic topological order (dependencies
// first). Ties break by registration order so runs are reproducible. A
// dependency cycle fails with the full loop spelled out, before any
// module is touched (spec §4.4 step 5).
func (g *graph) startOrder() ([]*node, error) {
	const (
		white = 0 // unvisited
		grey  = 1 // on the current path
		black = 2 // done
	)
	color := make(map[string]int, len(g.byName))
	var path []string
	var ordered []*node

	var visit func(*node) error
	visit = func(n *node) error {
		name := n.mod.Name()
		switch color[name] {
		case black:
			return nil
		case grey:
			// Spell out the loop for diagnostics (spec: 清晰的环路信息).
			start := 0
			for i, p := range path {
				if p == name {
					start = i
					break
				}
			}
			loop := append(append([]string{}, path[start:]...), name)
			return fmt.Errorf("runtime: dependency cycle: %s", strings.Join(loop, " -> "))
		}
		color[name] = grey
		path = append(path, name)
		for _, d := range n.info.Dependencies {
			dep, ok := g.byName[d.Name]
			if !ok {
				continue // optional and absent; required absence already rejected
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		path = path[:len(path)-1]
		color[name] = black
		ordered = append(ordered, n)
		return nil
	}

	for _, n := range g.order {
		if err := visit(n); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}
