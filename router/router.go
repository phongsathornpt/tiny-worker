package router

import (
	"sort"
	"strings"

	tinyworker "github.com/phongsathornpt/tiny-worker"
)

type node struct {
	static   map[string]*node
	param    *node
	paramKey string
	handlers map[string]tinyworker.Handler
}

type Router struct{ root node }

func New() *Router { return &Router{} }

func (r *Router) Handle(method, path string, handler tinyworker.Handler) {
	n := &r.root
	for _, part := range split(path) {
		if strings.HasPrefix(part, ":") {
			if n.param == nil {
				n.param = &node{}
			}
			n = n.param
			n.paramKey = part[1:]
			continue
		}
		if n.static == nil {
			n.static = make(map[string]*node)
		}
		next := n.static[part]
		if next == nil {
			next = &node{}
			n.static[part] = next
		}
		n = next
	}
	if n.handlers == nil {
		n.handlers = make(map[string]tinyworker.Handler)
	}
	n.handlers[method] = handler
}

// Lookup finds the handler for method+path and distinguishes the two miss
// cases:
//
//   - no node matches the path at all → ErrNotFound (404)
//   - the node exists but has no handler for the method → a
//     tinyworker.MethodNotAllowed (405) carrying the methods registered at
//     that node in its Allow header
func (r *Router) Lookup(method, path string) (tinyworker.Handler, []tinyworker.Param, error) {
	parts := split(path)
	params := make([]tinyworker.Param, 0, 2)
	h, allowed, ok := lookupNode(&r.root, method, parts, 0, &params)
	if !ok {
		return nil, nil, tinyworker.ErrNotFound
	}
	if h == nil {
		return nil, nil, tinyworker.MethodNotAllowed(allowed...)
	}
	return h, params, nil
}

func lookupNode(n *node, method string, parts []string, index int, params *[]tinyworker.Param) (tinyworker.Handler, []string, bool) {
	if index == len(parts) {
		if h := n.handlers[method]; h != nil {
			return h, nil, true
		}
		if len(n.handlers) == 0 {
			return nil, nil, false
		}
		return nil, allowedMethods(n.handlers), true
	}
	if child := n.static[parts[index]]; child != nil {
		if h, allowed, ok := lookupNode(child, method, parts, index+1, params); ok {
			return h, allowed, true
		}
	}
	if n.param != nil {
		mark := len(*params)
		*params = append(*params, tinyworker.Param{Name: n.param.paramKey, Value: parts[index]})
		if h, allowed, ok := lookupNode(n.param, method, parts, index+1, params); ok {
			return h, allowed, true
		}
		*params = (*params)[:mark]
	}
	return nil, nil, false
}

// Match keeps the original behavior: a single handler or nil, with no
// distinction between path misses and method misses.
func (r *Router) Match(method, path string) (tinyworker.Handler, []tinyworker.Param) {
	parts := split(path)
	params := make([]tinyworker.Param, 0, 2)
	h, ok := matchNode(&r.root, method, parts, 0, &params)
	if !ok {
		return nil, nil
	}
	return h, params
}

func matchNode(n *node, method string, parts []string, index int, params *[]tinyworker.Param) (tinyworker.Handler, bool) {
	if index == len(parts) {
		h := n.handlers[method]
		return h, h != nil
	}
	if child := n.static[parts[index]]; child != nil {
		if h, ok := matchNode(child, method, parts, index+1, params); ok {
			return h, true
		}
	}
	if n.param != nil {
		mark := len(*params)
		*params = append(*params, tinyworker.Param{Name: n.param.paramKey, Value: parts[index]})
		if h, ok := matchNode(n.param, method, parts, index+1, params); ok {
			return h, true
		}
		*params = (*params)[:mark]
	}
	return nil, false
}

func allowedMethods(handlers map[string]tinyworker.Handler) []string {
	methods := make([]string, 0, len(handlers)+1)
	for m := range handlers {
		methods = append(methods, m)
	}
	if _, ok := handlers["GET"]; ok {
		if _, ok := handlers["HEAD"]; !ok {
			methods = append(methods, "HEAD")
		}
	}
	sort.Strings(methods)
	return methods
}

func split(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}
