package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// handWritten are exported or package-level names declared outside generated files.
var handWritten = []string{
	"Client", "NewClient", "Option", "WithStoreID", "WithBaseURL", "WithHTTPClient", "WithUserAgent",
	"APIError", "Ptr", "UnrecognizedVariant", "DefaultBaseURL",
	"Webhook", "WebhookType", "WebhookVerifier", "NewWebhookVerifier", "VerifyWebhook", "SignWebhook", "WebhookTolerance",
	"WebhookTransaction", "WebhookTransactionData", "WebhookBillingKey", "WebhookBillingKeyData",
}

const unstableNote = "실험적 API입니다. 하위호환성 정책과 무관하게 변경 및 지원 종료될 수 있으니 이용에 유의하세요."

type gen struct {
	m     *model
	names map[string]string // Go identifier → what declared it, to catch collisions
	files map[string]*bytes.Buffer
}

func newGen(m *model) *gen {
	g := &gen{m: m, names: map[string]string{}, files: map[string]*bytes.Buffer{}}
	for _, n := range handWritten {
		g.names[n] = "hand-written code"
	}
	return g
}

func (g *gen) declare(name, by string) {
	if prev, ok := g.names[name]; ok {
		panic(fmt.Sprintf("identifier %s declared by both %s and %s", name, prev, by))
	}
	g.names[name] = by
}

func (g *gen) file(name string) *bytes.Buffer {
	if g.files[name] == nil {
		g.files[name] = &bytes.Buffer{}
	}
	return g.files[name]
}

func (g *gen) run() {
	for _, name := range g.m.order {
		cat, ok := g.m.category[name]
		if !ok {
			continue
		}
		g.genSchema(g.file(snake(cat)+"_types.gen.go"), name)
	}
	g.genClients()
	g.genRoot(g.file("portone.gen.go"))
	g.genUnionTest(g.file("portone.gen_test.go"))
}

// genUnionTest checks that every variant of every one-of survives json.Marshal and the one-of's decoder.
func (g *gen) genUnionTest(b *bytes.Buffer) {
	b.WriteString("func TestGeneratedUnionsRoundTrip(t *testing.T) {\n")
	b.WriteString("\ttests := []struct {\n\t\tname   string\n\t\tgive   any\n\t\tdecode func([]byte) (any, error)\n\t}{\n")
	for _, name := range g.m.order {
		u, ok := g.m.unions[name]
		if _, generated := g.m.category[name]; !ok || !generated {
			continue
		}
		for _, v := range u.Variants {
			fmt.Fprintf(b, "\t\t{%q, &%s{}, func(b []byte) (any, error) { return unmarshal%s(b) }},\n",
				typeName(u.Name)+"/"+typeName(v.Type), typeName(v.Type), typeName(u.Name))
		}
	}
	b.WriteString(`	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.give)
			if err != nil {
				t.Fatal(err)
			}
			got, err := tt.decode(raw)
			if err != nil {
				t.Fatalf("decode(%s): %v", raw, err)
			}
			if reflect.TypeOf(got) != reflect.TypeOf(tt.give) {
				t.Errorf("decode(%s) = %T, want %T", raw, got, tt.give)
			}
		})
	}
}
`)
}

// ---- doc comments ----

// paragraphs splits a description into paragraphs, dropping blanks and ones already said.
func paragraphs(texts ...string) []string {
	var out []string
	for _, t := range texts {
		t = strings.ReplaceAll(t, "\r", "")
		for _, p := range strings.Split(t, "\n\n") {
			p = strings.TrimSpace(p)
			if p != "" && !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	return out
}

func writeDoc(b *bytes.Buffer, indent string, paras []string) {
	for i, p := range paras {
		if i > 0 {
			fmt.Fprintf(b, "%s//\n", indent)
		}
		for _, line := range strings.Split(p, "\n") {
			fmt.Fprintf(b, "%s// %s\n", indent, strings.TrimRight(line, " \t"))
		}
	}
}

// typeDoc starts the doc with the identifier, as Go doc comments do.
func typeDoc(name, title, desc string) []string {
	if title == name || strings.ReplaceAll(title, "_", "") == name {
		title = ""
	}
	paras := paragraphs(title, desc)
	if len(paras) == 0 {
		return []string{name}
	}
	paras[0] = name + " " + paras[0]
	return paras
}

// ---- types ----

// goType returns the Go type of a schema used as a field or parameter.
func (g *gen) goType(s *Schema, optional bool) string {
	ptr := func(t string) string {
		if optional {
			return "*" + t
		}
		return t
	}
	if s.Ref != "" {
		name := refName(s.Ref)
		switch g.m.kind(name) {
		case "union", "map":
			return typeName(name)
		default:
			return ptr(typeName(name))
		}
	}
	switch s.Type {
	case "string":
		if s.Format == "date-time" {
			return ptr("time.Time")
		}
		return ptr("string")
	case "integer":
		if s.Format == "int32" {
			return ptr("int32")
		}
		return ptr("int64")
	case "number":
		return ptr("float64")
	case "boolean":
		return ptr("bool")
	case "array":
		return "[]" + g.goType(s.Items, false)
	case "object":
		if len(s.Properties) > 0 {
			panic("inline object with properties is not supported")
		}
		if s.AdditionalProperties != nil {
			return "map[string]" + g.goType(s.AdditionalProperties, false)
		}
		return "map[string]any"
	}
	panic(fmt.Sprintf("unsupported schema type %q", s.Type))
}

// unionField reports whether a field holds a union (directly or as a slice), which needs custom decoding.
func (g *gen) unionField(s *Schema) (name string, slice bool) {
	if s.Ref != "" && g.m.kind(refName(s.Ref)) == "union" {
		return refName(s.Ref), false
	}
	if s.Type == "array" && s.Items.Ref != "" && g.m.kind(refName(s.Items.Ref)) == "union" {
		return refName(s.Items.Ref), true
	}
	if s.AdditionalProperties != nil {
		if n, _ := g.unionField(s.AdditionalProperties); n != "" {
			panic("map of unions is not supported")
		}
	}
	return "", false
}

type field struct {
	JSON, Go, Type string
	Required       bool
	Schema         *Schema
}

func (g *gen) fields(name string) []field {
	s := g.m.schemas[name]
	required := map[string]bool{}
	for _, r := range s.Required {
		required[r] = true
	}
	var out []field
	seen := map[string]bool{}
	for _, p := range s.Properties {
		if p.Key == g.m.discrProp[name] {
			continue // written by MarshalJSON; the Go type already tells the variant
		}
		f := field{JSON: p.Key, Go: exported(p.Key), Required: required[p.Key], Schema: p.Value}
		f.Type = g.goType(p.Value, !f.Required)
		if f.Go == "MarshalJSON" || f.Go == "UnmarshalJSON" || (f.Go == "Error" && g.isError(name)) {
			panic(fmt.Sprintf("%s.%s collides with a generated method", name, f.Go))
		}
		if seen[f.Go] {
			panic(fmt.Sprintf("%s: duplicate field %s", name, f.Go))
		}
		seen[f.Go] = true
		out = append(out, f)
	}
	return out
}

func (g *gen) genSchema(b *bytes.Buffer, name string) {
	s := g.m.schemas[name]
	tn := typeName(name)
	g.declare(tn, "schema "+name)
	switch g.m.kind(name) {
	case "union":
		g.genUnion(b, g.m.unions[name])
	case "enum":
		g.genEnum(b, name, s)
	case "map":
		writeDoc(b, "", typeDoc(tn, s.Title, s.Description))
		fmt.Fprintf(b, "type %s map[string]%s\n\n", tn, g.goType(s.AdditionalProperties, false))
	case "object":
		g.genObject(b, name, s)
	default:
		panic(fmt.Sprintf("schema %s: unsupported top-level kind %s", name, g.m.kind(name)))
	}
}

func (g *gen) genEnum(b *bytes.Buffer, name string, s *Schema) {
	tn := typeName(name)
	writeDoc(b, "", typeDoc(tn, s.Title, s.Description))
	fmt.Fprintf(b, "type %s string\n\nconst (\n", tn)
	for _, v := range s.Enum {
		c := enumConst(tn, v)
		g.declare(c, "enum "+name)
		meta := s.EnumMeta[v]
		writeDoc(b, "\t", typeDoc(c, meta.Title, meta.Description))
		fmt.Fprintf(b, "\t%s %s = %q\n", c, tn, v)
	}
	b.WriteString(")\n\n")
}

func (g *gen) genObject(b *bytes.Buffer, name string, s *Schema) {
	tn := typeName(name)
	fields := g.fields(name)
	paras := typeDoc(tn, s.Title, s.Description)
	if unions := g.m.variantOf[name]; len(unions) > 0 {
		var names []string
		for _, u := range unions {
			names = append(names, "["+typeName(u.Name)+"]")
		}
		paras = append(paras, fmt.Sprintf("%s is a variant of %s, identified by %s %q.",
			tn, strings.Join(names, ", "), unions[0].Prop, g.m.discr[name].Value))
	}
	writeDoc(b, "", paras)
	fmt.Fprintf(b, "type %s struct {\n", tn)
	for _, f := range fields {
		writeDoc(b, "\t", paragraphs(f.Schema.Title, f.Schema.Description))
		tag := f.JSON
		if !f.Required {
			tag += ",omitempty"
		}
		fmt.Fprintf(b, "\t%s %s `json:%q`\n", f.Go, f.Type, tag)
	}
	b.WriteString("}\n\n")

	if v, ok := g.m.discr[name]; ok {
		for _, u := range g.m.variantOf[name] {
			fmt.Fprintf(b, "func (%s) is%s() {}\n\n", tn, typeName(u.Name))
		}
		fmt.Fprintf(b, "// MarshalJSON writes the %q discriminator along with the fields.\n", g.m.discrProp[name])
		fmt.Fprintf(b, "func (v %s) MarshalJSON() ([]byte, error) {\n", tn)
		fmt.Fprintf(b, "\ttype alias %s\n", tn)
		fmt.Fprintf(b, "\treturn json.Marshal(struct {\n\t\tDiscriminator string `json:%q`\n\t\talias\n\t}{%q, alias(v)})\n}\n\n",
			g.m.discrProp[name], v.Value)
		if g.isError(name) {
			msg := "nil"
			for _, f := range fields {
				if f.JSON == "message" {
					msg = "e.Message"
					if f.Required {
						msg = "&e.Message"
					}
				}
			}
			fmt.Fprintf(b, "func (e *%s) Error() string { return errorString(%q, %s) }\n\n", tn, v.Value, msg)
		}
	}

	var unionFields []field
	for _, f := range fields {
		if u, _ := g.unionField(f.Schema); u != "" {
			unionFields = append(unionFields, f)
		}
	}
	if len(unionFields) == 0 {
		return
	}
	fmt.Fprintf(b, "// UnmarshalJSON decodes the union-typed fields into their variants.\n")
	fmt.Fprintf(b, "func (v *%s) UnmarshalJSON(b []byte) error {\n\ttype alias %s\n\tvar raw struct {\n\t\t*alias\n", tn, tn)
	for _, f := range unionFields {
		if _, slice := g.unionField(f.Schema); slice {
			fmt.Fprintf(b, "\t\t%s []json.RawMessage `json:%q`\n", f.Go, f.JSON)
		} else {
			fmt.Fprintf(b, "\t\t%s json.RawMessage `json:%q`\n", f.Go, f.JSON)
		}
	}
	b.WriteString("\t}\n\traw.alias = (*alias)(v)\n\tif err := json.Unmarshal(b, &raw); err != nil {\n\t\treturn err\n\t}\n\tvar err error\n")
	for _, f := range unionFields {
		u, slice := g.unionField(f.Schema)
		fn := "unmarshal" + typeName(u)
		if slice {
			fmt.Fprintf(b, "\tif v.%s, err = unmarshalSlice(raw.%s, %s); err != nil {\n\t\treturn err\n\t}\n", f.Go, f.Go, fn)
		} else {
			fmt.Fprintf(b, "\tif v.%s, err = %s(raw.%s); err != nil {\n\t\treturn err\n\t}\n", f.Go, fn, f.Go)
		}
	}
	b.WriteString("\treturn nil\n}\n\n")
}

func (g *gen) isError(name string) bool {
	for _, v := range g.m.errors {
		if v.Type == name {
			return true
		}
	}
	return false
}

func (g *gen) genUnion(b *bytes.Buffer, u *union) {
	tn := typeName(u.Name)
	var variants []string
	for _, v := range u.Variants {
		variants = append(variants, "[*"+typeName(v.Type)+"]")
	}
	paras := typeDoc(tn, u.Doc.Title, u.Doc.Description)
	paras = append(paras, fmt.Sprintf("Decoded values are one of %s, chosen by %q, or [*UnrecognizedVariant] for a %s this SDK version does not know.",
		strings.Join(variants, ", "), u.Prop, u.Prop))
	writeDoc(b, "", paras)
	if g.m.errUnions[u.Name] {
		fmt.Fprintf(b, "type %s interface {\n\terror\n\tis%s()\n}\n\n", tn, tn)
	} else {
		fmt.Fprintf(b, "type %s interface {\n\tis%s()\n}\n\n", tn, tn)
	}

	fmt.Fprintf(b, "func unmarshal%s(b []byte) (%s, error) {\n", tn, tn)
	fmt.Fprintf(b, "\tvalue, isNull, err := readDiscriminator(b, %q)\n\tif err != nil || isNull {\n\t\treturn nil, err\n\t}\n\tswitch value {\n", u.Prop)
	for _, v := range u.Variants {
		fmt.Fprintf(b, "\tcase %q:\n\t\treturn decodeVariant[%s, %s](b)\n", v.Value, typeName(v.Type), tn)
	}
	b.WriteString("\t}\n\treturn newUnrecognizedVariant(value, b), nil\n}\n\n")
}

// ---- clients ----

type clientNode struct {
	path     []string
	ops      []*op
	children []*clientNode
}

func (n *clientNode) typeName() string {
	var b strings.Builder
	for _, p := range n.path {
		b.WriteString(exported(p))
	}
	return b.String() + "Client"
}

func (n *clientNode) child(name string) *clientNode {
	for _, c := range n.children {
		if c.path[len(c.path)-1] == name {
			return c
		}
	}
	c := &clientNode{path: append(slices.Clone(n.path), name)}
	n.children = append(n.children, c)
	sort.Slice(n.children, func(i, j int) bool {
		return strings.Join(n.children[i].path, ".") < strings.Join(n.children[j].path, ".")
	})
	return c
}

func (g *gen) clientTree() *clientNode {
	root := &clientNode{}
	for _, o := range g.m.ops {
		n := root
		for _, p := range strings.Split(o.Category, ".") {
			n = n.child(p)
		}
		n.ops = append(n.ops, o)
	}
	return root
}

func (g *gen) genClients() {
	var walk func(n *clientNode)
	walk = func(n *clientNode) {
		g.genClient(g.file(snake(strings.Join(n.path, "."))+"_client.gen.go"), n)
		for _, c := range n.children {
			walk(c)
		}
	}
	for _, c := range g.clientTree().children {
		walk(c)
	}
}

func (g *gen) genClient(b *bytes.Buffer, n *clientNode) {
	tn := n.typeName()
	g.declare(tn, "category "+strings.Join(n.path, "."))
	fmt.Fprintf(b, "// %s calls the %s APIs. Get it from [Client].\n", tn, strings.Join(n.path, "."))
	fmt.Fprintf(b, "type %s struct {\n\tcfg *config\n", tn)
	for _, c := range n.children {
		fmt.Fprintf(b, "\t%s *%s\n", exported(c.path[len(c.path)-1]), c.typeName())
	}
	b.WriteString("}\n\n")
	fmt.Fprintf(b, "func new%s(cfg *config) *%s {\n\treturn &%s{\n\t\tcfg: cfg,\n", tn, tn, tn)
	for _, c := range n.children {
		fmt.Fprintf(b, "\t\t%s: new%s(cfg),\n", exported(c.path[len(c.path)-1]), c.typeName())
	}
	b.WriteString("\t}\n}\n\n")
	for _, o := range n.ops {
		g.genOp(b, tn, o)
	}
}

// reservedLocals are names used inside generated methods, which a path parameter must not shadow.
var reservedLocals = map[string]bool{
	"ctx": true, "body": true, "params": true, "query": true, "raw": true, "err": true, "c": true,
	"url": true, "http": true, "json": true, "time": true, "strconv": true, "context": true,
}

var pathParam = regexp.MustCompile(`\{([^}]+)\}`)

func (g *gen) genOp(b *bytes.Buffer, client string, o *op) {
	method := exported(o.ID)

	paramsType := ""
	paramsByValue := false
	if len(o.QueryParams) > 0 {
		paramsType = method + "Params"
		g.declare(paramsType, "operation "+o.ID)
		fmt.Fprintf(b, "// %s holds the query parameters of [%s.%s].\n", paramsType, client, method)
		fmt.Fprintf(b, "type %s struct {\n", paramsType)
		for _, p := range o.QueryParams {
			writeDoc(b, "\t", paragraphs(p.Title, p.Description))
			fmt.Fprintf(b, "\t%s %s\n", exported(p.Name), g.goType(p.Schema, !p.Required))
			paramsByValue = paramsByValue || p.Required
		}
		b.WriteString("}\n\n")
	}

	// Signature.
	args := []string{"ctx context.Context"}
	locals := map[string]string{}
	for _, p := range o.PathParams {
		l := local(p.Name)
		if reservedLocals[l] {
			l += "Param"
		}
		locals[p.Name] = l
		args = append(args, l+" "+g.goType(p.Schema, false))
	}
	if o.Body != "" {
		args = append(args, "body "+typeName(o.Body))
	}
	if paramsType != "" {
		if paramsByValue {
			args = append(args, "params "+paramsType)
		} else {
			args = append(args, "params *"+paramsType)
		}
	}
	var result, zero string
	switch {
	case o.CSV:
		result, zero = "([]byte, error)", "nil, "
	case o.Response == "":
		result, zero = "error", ""
	case g.m.kind(o.Response) == "union":
		result, zero = "("+typeName(o.Response)+", error)", "nil, "
	default:
		result, zero = "(*"+typeName(o.Response)+", error)", "nil, "
	}

	// Doc.
	paras := paragraphs(o.Summary, o.Doc)
	if len(paras) == 0 {
		paras = []string{method}
	} else {
		paras[0] = method + " " + paras[0]
	}
	if o.Unstable {
		paras = append(paras, unstableNote)
	}
	if o.StoreQuery || g.hasStoreID(o.Body) {
		paras = append(paras, "The store ID comes from [WithStoreID] unless the request sets one.")
	}
	var errs []string
	for _, v := range g.m.unions[o.ErrorUnion].Variants {
		errs = append(errs, "[*"+typeName(v.Type)+"]")
	}
	paras = append(paras, "API errors are returned as [*APIError] wrapping a ["+typeName(o.ErrorUnion)+"]: "+strings.Join(errs, ", ")+".")
	writeDoc(b, "", paras)
	fmt.Fprintf(b, "func (c *%s) %s(%s) %s {\n", client, method, strings.Join(args, ", "), result)

	// Query.
	queryArg := "nil"
	if o.StoreQuery || paramsType != "" || o.BodyInQuery {
		queryArg = "query"
		b.WriteString("\tquery := url.Values{}\n")
	}
	if o.StoreQuery {
		b.WriteString("\tc.cfg.setStoreID(query)\n")
	}
	if paramsType != "" {
		indent := "\t"
		if !paramsByValue {
			b.WriteString("\tif params != nil {\n")
			indent = "\t\t"
		}
		for _, p := range o.QueryParams {
			f := "params." + exported(p.Name)
			if p.Required {
				fmt.Fprintf(b, "%squery.Set(%q, %s)\n", indent, p.Name, g.queryString(p.Schema, f))
			} else {
				fmt.Fprintf(b, "%sif %s != nil {\n%s\tquery.Set(%q, %s)\n%s}\n", indent, f, indent, p.Name, g.queryString(p.Schema, "*"+f), indent)
			}
		}
		if !paramsByValue {
			b.WriteString("\t}\n")
		}
	}

	// Body.
	bodyArg := "nil"
	if o.Body != "" {
		if g.hasStoreID(o.Body) {
			if g.storeIDRequired(o.Body) {
				b.WriteString("\tif body.StoreID == \"\" {\n\t\tbody.StoreID = c.cfg.storeID\n\t}\n")
			} else {
				b.WriteString("\tif body.StoreID == nil {\n\t\tbody.StoreID = c.cfg.storeIDPtr()\n\t}\n")
			}
		}
		if o.BodyInQuery {
			fmt.Fprintf(b, "\tif err := setJSONQuery(query, \"requestBody\", body); err != nil {\n\t\treturn %serr\n\t}\n", zero)
		} else {
			bodyArg = "body"
		}
	}

	// Path.
	var path strings.Builder
	last := 0
	for _, m := range pathParam.FindAllStringSubmatchIndex(o.Path, -1) {
		if m[0] > last {
			path.WriteString(strconv.Quote(o.Path[last:m[0]]) + " + ")
		}
		name := o.Path[m[2]:m[3]]
		arg := locals[name]
		for _, p := range o.PathParams {
			if p.Name == name && p.Schema.Ref != "" {
				arg = "string(" + arg + ")"
			}
		}
		path.WriteString("url.PathEscape(" + arg + ")")
		last = m[1]
		if last < len(o.Path) {
			path.WriteString(" + ")
		}
	}
	if last < len(o.Path) {
		path.WriteString(strconv.Quote(o.Path[last:]))
	}

	call := fmt.Sprintf("c.cfg.do(ctx, http.Method%s, %s, %s, %s, errorDecoder(unmarshal%s))",
		capitalize(strings.ToLower(o.Method)), path.String(), queryArg, bodyArg, typeName(o.ErrorUnion))
	switch {
	case o.CSV:
		fmt.Fprintf(b, "\treturn %s\n}\n\n", call)
	case o.Response == "":
		fmt.Fprintf(b, "\t_, err := %s\n\treturn err\n}\n\n", call)
	default:
		fmt.Fprintf(b, "\traw, err := %s\n\tif err != nil {\n\t\treturn nil, err\n\t}\n", call)
		if g.m.kind(o.Response) == "union" {
			fmt.Fprintf(b, "\treturn decodeResponse(raw, unmarshal%s)\n}\n\n", typeName(o.Response))
		} else {
			fmt.Fprintf(b, "\treturn decodeResponse(raw, decodeObject[%s])\n}\n\n", typeName(o.Response))
		}
	}
}

func (g *gen) storeIDProp(body string) (*Schema, bool) {
	if body == "" {
		return nil, false
	}
	s := g.m.schemas[body]
	for _, p := range s.Properties {
		if p.Key == "storeId" {
			return p.Value, slices.Contains(s.Required, "storeId")
		}
	}
	return nil, false
}

func (g *gen) hasStoreID(body string) bool {
	s, _ := g.storeIDProp(body)
	return s != nil && s.Type == "string"
}

func (g *gen) storeIDRequired(body string) bool {
	_, req := g.storeIDProp(body)
	return req
}

func (g *gen) queryString(s *Schema, expr string) string {
	if s.Ref != "" {
		if g.m.kind(refName(s.Ref)) != "enum" {
			panic("query parameter of type " + refName(s.Ref) + " is not supported")
		}
		return "string(" + expr + ")"
	}
	switch s.Type {
	case "string":
		if s.Format == "date-time" {
			return expr + ".Format(time.RFC3339)"
		}
		return expr
	case "boolean":
		return "strconv.FormatBool(" + expr + ")"
	case "integer":
		return "strconv.FormatInt(int64(" + expr + "), 10)"
	case "number":
		return "strconv.FormatFloat(" + expr + ", 'f', -1, 64)"
	}
	panic("query parameter of type " + s.Type + " is not supported")
}

// ---- root ----

func (g *gen) genRoot(b *bytes.Buffer) {
	fmt.Fprintf(b, "// SpecVersion is the PortOne OpenAPI version this SDK was generated from.\nconst SpecVersion = %q\n\n", g.m.version)
	g.declare("SpecVersion", "root")

	root := g.clientTree()
	b.WriteString("// Client calls the PortOne V2 REST API. Create it with [NewClient]; it is safe for concurrent use.\n")
	b.WriteString("type Client struct {\n")
	for i, c := range root.children {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(b, "\t%s *%s\n", exported(c.path[0]), c.typeName())
	}
	b.WriteString("}\n\nfunc newClient(cfg *config) *Client {\n\treturn &Client{\n")
	for _, c := range root.children {
		fmt.Fprintf(b, "\t\t%s: new%s(cfg),\n", exported(c.path[0]), c.typeName())
	}
	b.WriteString("\t}\n}\n\n")

	var unions []string
	for name := range g.m.unions {
		if _, ok := g.m.category[name]; ok {
			unions = append(unions, typeName(name))
		}
	}
	sort.Strings(unions)
	for _, u := range unions {
		fmt.Fprintf(b, "func (*UnrecognizedVariant) is%s() {}\n", u)
	}
}

// ---- output ----

var _stdImports = map[string]string{
	"context": "context", "json": "encoding/json", "http": "net/http",
	"url": "net/url", "strconv": "strconv", "time": "time",
	"reflect": "reflect", "testing": "testing",
}

func render(name, version string, body []byte) ([]byte, error) {
	src := []byte("package portone\n\n" + string(body))
	f, err := parser.ParseFile(token.NewFileSet(), name, src, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: %w\n%s", name, err, src)
	}
	used := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && _stdImports[id.Name] != "" {
				used[_stdImports[id.Name]] = true
			}
		}
		return true
	})
	var imports []string
	for p := range used {
		imports = append(imports, strconv.Quote(p))
	}
	sort.Strings(imports)

	var out bytes.Buffer
	fmt.Fprintf(&out, "// Code generated by internal/codegen from PortOne OpenAPI %s. DO NOT EDIT.\n\npackage portone\n\n", version)
	if len(imports) > 0 {
		fmt.Fprintf(&out, "import (\n%s\n)\n\n", strings.Join(imports, "\n"))
	}
	out.Write(body)
	formatted, err := format.Source(out.Bytes())
	if err != nil {
		return nil, fmt.Errorf("%s: format: %w", name, err)
	}
	return formatted, nil
}
