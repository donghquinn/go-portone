package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// excludedOperations mirrors upstream's codegen, which leaves these out of every SDK.
var excludedOperations = map[string]bool{"getBankInfos": true}

type union struct {
	Name     string
	Prop     string // discriminator property, e.g. "status"
	Variants []variant
	Doc      *Schema
}

type variant struct {
	Value string // discriminator value, e.g. "PAID"
	Type  string // schema name, e.g. "PaidPayment"
}

type op struct {
	ID       string
	Method   string // GET, POST, ...
	Path     string
	Category string
	Summary  string
	Doc      string
	Unstable bool

	PathParams  []*Parameter
	QueryParams []*Parameter // excludes storeId and requestBody
	StoreQuery  bool         // has a storeId query parameter
	Body        string       // body schema name; "" when none or empty
	BodyInQuery bool         // GET/DELETE: body is sent as the requestBody query parameter

	Response   string // response schema name
	CSV        bool
	ErrorUnion string
}

type model struct {
	version   string
	schemas   map[string]*Schema
	order     []string
	unions    map[string]*union
	variantOf map[string][]*union
	discr     map[string]variant // variant schema → its discriminator value
	discrProp map[string]string  // variant schema → discriminator property
	ops       []*op
	category  map[string]string // schema → category it is generated in
	errors    []variant         // all error variants reachable from operations, sorted by type
	errUnions map[string]bool   // unions used as an operation's error type
}

func buildModel(spec *Spec) (*model, error) {
	m := &model{
		version:   spec.Info.Version,
		schemas:   map[string]*Schema{},
		unions:    map[string]*union{},
		variantOf: map[string][]*union{},
		discr:     map[string]variant{},
		discrProp: map[string]string{},
		category:  map[string]string{},
		errUnions: map[string]bool{},
	}
	for _, e := range spec.Components.Schemas {
		m.schemas[e.Key] = e.Value
		m.order = append(m.order, e.Key)
	}

	for _, name := range m.order {
		s := m.schemas[name]
		if s.OneOf == nil {
			continue
		}
		if s.Discriminator == nil {
			return nil, fmt.Errorf("%s: oneOf without discriminator", name)
		}
		u := &union{Name: name, Prop: s.Discriminator.PropertyName, Doc: s}
		for _, e := range s.Discriminator.Mapping {
			v := variant{Value: e.Key, Type: refName(e.Value)}
			target, ok := m.schemas[v.Type]
			if !ok || target.Type != "object" || target.OneOf != nil {
				return nil, fmt.Errorf("%s: variant %s is not an object", name, v.Type)
			}
			if prev, ok := m.discr[v.Type]; ok && (prev.Value != v.Value || m.discrProp[v.Type] != u.Prop) {
				return nil, fmt.Errorf("%s: variant %s has conflicting discriminators", name, v.Type)
			}
			m.discr[v.Type] = v
			m.discrProp[v.Type] = u.Prop
			u.Variants = append(u.Variants, v)
			m.variantOf[v.Type] = append(m.variantOf[v.Type], u)
		}
		m.unions[name] = u
	}

	for _, p := range spec.Paths {
		for _, e := range p.Value {
			o, err := m.buildOp(p.Key, e.Key, e.Value)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", e.Key, p.Key, err)
			}
			if o != nil {
				m.ops = append(m.ops, o)
			}
		}
	}

	m.assignCategories()

	seen := map[string]bool{}
	for _, o := range m.ops {
		m.errUnions[o.ErrorUnion] = true
		for _, v := range m.unions[o.ErrorUnion].Variants {
			if seen[v.Type] {
				continue
			}
			seen[v.Type] = true
			m.errors = append(m.errors, v)
		}
	}
	sort.Slice(m.errors, func(i, j int) bool { return m.errors[i].Type < m.errors[j].Type })
	return m, nil
}

func (m *model) buildOp(path, method string, o *Operation) (*op, error) {
	if excludedOperations[o.OperationID] {
		return nil, nil
	}
	res := &op{
		ID:         o.OperationID,
		Method:     strings.ToUpper(method),
		Path:       path,
		Category:   o.Category,
		Summary:    o.Summary,
		Doc:        o.Description,
		Unstable:   o.Unstable,
		ErrorUnion: refName(o.Error.Ref),
	}
	if res.Category == "" {
		res.Category = "platform" // upstream's normalizeCategory
	}
	if _, ok := m.unions[res.ErrorUnion]; !ok {
		return nil, fmt.Errorf("error %s is not a union", res.ErrorUnion)
	}

	var body *Schema
	if o.RequestBody != nil {
		mt, ok := o.RequestBody.Content["application/json"]
		if !ok {
			return nil, fmt.Errorf("request body is not JSON")
		}
		body = mt.Schema
	}
	for _, p := range o.Parameters {
		switch {
		case p.Content != nil:
			mt, ok := p.Content["application/json"]
			if !ok {
				return nil, fmt.Errorf("parameter %s is not JSON", p.Name)
			}
			body = mt.Schema
		case p.In == "path":
			if p.Schema.Type != "string" && (p.Schema.Ref == "" || m.schemas[refName(p.Schema.Ref)].Enum == nil) {
				return nil, fmt.Errorf("path parameter %s is not a string", p.Name)
			}
			res.PathParams = append(res.PathParams, p)
		case p.In == "query" && p.Name == "storeId":
			res.StoreQuery = true
		case p.In == "query":
			res.QueryParams = append(res.QueryParams, p)
		default:
			return nil, fmt.Errorf("unsupported parameter %s in %s", p.Name, p.In)
		}
	}
	if body != nil {
		if body.Ref == "" {
			return nil, fmt.Errorf("inline request body")
		}
		name := refName(body.Ref)
		// Like upstream, an empty body object is not sent at all.
		if len(m.schemas[name].Properties) > 0 {
			res.Body = name
			res.BodyInQuery = res.Method == "GET" || res.Method == "DELETE"
		}
	}

	if r, ok := o.Responses["200"]; ok && r.Content != nil {
		switch {
		case r.Content["application/json"] != nil:
			s := r.Content["application/json"].Schema
			if s.Ref == "" {
				return nil, fmt.Errorf("inline response")
			}
			res.Response = refName(s.Ref)
		case r.Content["text/csv"] != nil:
			res.CSV = true
		default:
			return nil, fmt.Errorf("unsupported response content")
		}
	}
	return res, nil
}

// assignCategories places each reachable schema in the category of the operations using it, as upstream does:
// one category → that one; several platform.* → platform; otherwise common. Unreachable schemas are dropped.
func (m *model) assignCategories() {
	usage := map[string]map[string]bool{}
	var walk func(cat string, s *Schema)
	visit := func(cat, name string) {
		if usage[name] == nil {
			usage[name] = map[string]bool{}
		}
		if usage[name][cat] {
			return
		}
		usage[name][cat] = true
		walk(cat, m.schemas[name])
	}
	walk = func(cat string, s *Schema) {
		if s == nil {
			return
		}
		if s.Ref != "" {
			visit(cat, refName(s.Ref))
			return
		}
		for _, p := range s.Properties {
			walk(cat, p.Value)
		}
		walk(cat, s.Items)
		walk(cat, s.AdditionalProperties)
		if s.Discriminator != nil {
			for _, e := range s.Discriminator.Mapping {
				visit(cat, refName(e.Value))
			}
		}
	}
	for _, o := range m.ops {
		for _, p := range append(slices.Clone(o.PathParams), o.QueryParams...) {
			walk(o.Category, p.Schema)
		}
		for _, name := range []string{o.Body, o.Response, o.ErrorUnion} {
			if name != "" {
				visit(o.Category, name)
			}
		}
	}
	for name, cats := range usage {
		var list []string
		allPlatform := true
		for c := range cats {
			list = append(list, c)
			allPlatform = allPlatform && strings.HasPrefix(c, "platform")
		}
		switch {
		case len(list) == 1:
			m.category[name] = list[0]
		case allPlatform:
			m.category[name] = "platform"
		default:
			m.category[name] = "common"
		}
	}
}

// kind classifies a named schema for code generation.
func (m *model) kind(name string) string {
	s := m.schemas[name]
	switch {
	case s.OneOf != nil:
		return "union"
	case s.Enum != nil:
		return "enum"
	case s.Type == "object" && len(s.Properties) == 0 && s.AdditionalProperties != nil:
		return "map"
	case s.Type == "object":
		return "object"
	}
	return s.Type
}
