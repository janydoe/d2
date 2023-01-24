package d2compiler

import (
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"oss.terrastruct.com/util-go/go2"

	"oss.terrastruct.com/d2/d2ast"
	"oss.terrastruct.com/d2/d2format"
	"oss.terrastruct.com/d2/d2graph"
	"oss.terrastruct.com/d2/d2ir"
	"oss.terrastruct.com/d2/d2parser"
	"oss.terrastruct.com/d2/d2target"
)

type CompileOptions struct {
	UTF16 bool
}

func Compile(path string, r io.RuneReader, opts *CompileOptions) (*d2graph.Graph, error) {
	if opts == nil {
		opts = &CompileOptions{}
	}

	var pe d2parser.ParseError

	ast, err := d2parser.Parse(path, r, &d2parser.ParseOptions{
		UTF16: opts.UTF16,
	})
	if err != nil {
		return nil, err
	}

	ir, err := d2ir.Compile(ast)
	if err != nil {
		return nil, err
	}

	g, err := compileIR(pe, ir)
	if err != nil {
		return nil, err
	}
	g.AST = ast
	return g, err
}

func compileIR(pe d2parser.ParseError, m *d2ir.Map) (*d2graph.Graph, error) {
	c := &compiler{
		err: pe,
	}

	g := c.compileLayer(m)
	if len(c.err.Errors) > 0 {
		return nil, c.err
	}
	return g, nil
}

func (c *compiler) compileLayer(ir *d2ir.Map) *d2graph.Graph {
	g := d2graph.NewGraph()

	m := ir.CopyRoot()
	c.compileMap(g.Root, m)
	if len(c.err.Errors) == 0 {
		c.validateKeys(g.Root, m)
	}
	c.validateNear(g)

	c.compileLayersField(g, ir, "layers")
	c.compileLayersField(g, ir, "scenarios")
	c.compileLayersField(g, ir, "steps")
	return g
}

func (c *compiler) compileLayersField(g *d2graph.Graph, ir *d2ir.Map, fieldName string) {
	layers := ir.GetField(fieldName)
	if layers.Map() == nil {
		return
	}
	for _, f := range layers.Map().Fields {
		if f.Map() == nil {
			continue
		}
		g2 := c.compileLayer(f.Map())
		g2.Name = f.Name
		switch fieldName {
		case "layers":
			g.Layers = append(g.Layers, g2)
		case "scenarios":
			g.Scenarios = append(g.Scenarios, g2)
		case "steps":
			g.Steps = append(g.Steps, g2)
		}
	}
}

type compiler struct {
	err d2parser.ParseError
}

func (c *compiler) errorf(n d2ast.Node, f string, v ...interface{}) {
	c.err.Errors = append(c.err.Errors, d2parser.Errorf(n, f, v...).(d2ast.Error))
}

func (c *compiler) compileMap(obj *d2graph.Object, m *d2ir.Map) {
	shape := m.GetField("shape")
	if shape != nil {
		c.compileField(obj, shape)
	}
	for _, f := range m.Fields {
		if f.Name == "shape" {
			continue
		}
		c.compileField(obj, f)
	}

	switch obj.Attributes.Shape.Value {
	case d2target.ShapeClass:
		c.compileClass(obj)
	case d2target.ShapeSQLTable:
		c.compileSQLTable(obj)
	}

	for _, e := range m.Edges {
		c.compileEdge(obj, e)
	}
}

func (c *compiler) compileField(obj *d2graph.Object, f *d2ir.Field) {
	keyword := strings.ToLower(f.Name)
	_, isReserved := d2graph.SimpleReservedKeywords[keyword]
	if isReserved {
		c.compileReserved(obj.Attributes, f)
		return
	} else if f.Name == "style" {
		if f.Map() == nil {
			return
		}
		c.compileStyle(obj.Attributes, f.Map())
		if obj.Attributes.Style.Animated != nil {
			c.errorf(obj.Attributes.Style.Animated.MapKey, `key "animated" can only be applied to edges`)
		}
		return
	}

	obj = obj.EnsureChild(d2graphIDA([]string{f.Name}))
	if f.Primary() != nil {
		c.compileLabel(obj.Attributes, f)
	}
	if f.Map() != nil {
		c.compileMap(obj, f.Map())
	}

	for _, fr := range f.References {
		if fr.OurValue() && fr.Context.Key.Value.Map != nil {
			obj.Map = fr.Context.Key.Value.Map
		}
		scopeObjIDA := d2ir.IDA(fr.Context.ScopeMap)
		scopeObj, _ := obj.Graph.Root.HasChild(scopeObjIDA)
		obj.References = append(obj.References, d2graph.Reference{
			Key:          fr.KeyPath,
			KeyPathIndex: fr.KeyPathIndex(),

			MapKey:          fr.Context.Key,
			MapKeyEdgeIndex: fr.Context.EdgeIndex(),
			Scope:           fr.Context.Scope,
			ScopeObj:        scopeObj,
		})
	}
}

func (c *compiler) compileLabel(attrs *d2graph.Attributes, f d2ir.Node) {
	scalar := f.Primary().Value
	switch scalar := scalar.(type) {
	case *d2ast.Null:
		// TODO: Delete instaed.
		attrs.Label.Value = scalar.ScalarString()
	case *d2ast.BlockString:
		attrs.Language = scalar.Tag
		fullTag, ok := ShortToFullLanguageAliases[scalar.Tag]
		if ok {
			attrs.Language = fullTag
		}
		if attrs.Language == "markdown" || attrs.Language == "latex" {
			attrs.Shape.Value = d2target.ShapeText
		} else {
			attrs.Shape.Value = d2target.ShapeCode
		}
	default:
		attrs.Label.Value = scalar.ScalarString()
	}
	attrs.Label.MapKey = f.LastPrimaryKey()
}

func (c *compiler) compileReserved(attrs *d2graph.Attributes, f *d2ir.Field) {
	if f.Primary() == nil {
		if f.Composite != nil {
			c.errorf(f.LastPrimaryKey(), "reserved field %v does not accept composite", f.Name)
		}
		return
	}
	scalar := f.Primary().Value
	switch f.Name {
	case "label":
		c.compileLabel(attrs, f)
	case "shape":
		in := d2target.IsShape(scalar.ScalarString())
		_, isArrowhead := d2target.Arrowheads[scalar.ScalarString()]
		if !in && !isArrowhead {
			c.errorf(scalar, "unknown shape %q", scalar.ScalarString())
			return
		}
		attrs.Shape.Value = scalar.ScalarString()
		if attrs.Shape.Value == d2target.ShapeCode {
			// Explicit code shape is plaintext.
			attrs.Language = d2target.ShapeText
		}
		attrs.Shape.MapKey = f.LastPrimaryKey()
	case "icon":
		iconURL, err := url.Parse(scalar.ScalarString())
		if err != nil {
			c.errorf(scalar, "bad icon url %#v: %s", scalar.ScalarString(), err)
			return
		}
		attrs.Icon = iconURL
	case "near":
		nearKey, err := d2parser.ParseKey(scalar.ScalarString())
		if err != nil {
			c.errorf(scalar, "bad near key %#v: %s", scalar.ScalarString(), err)
			return
		}
		nearKey.Range = scalar.GetRange()
		attrs.NearKey = nearKey
	case "tooltip":
		attrs.Tooltip = scalar.ScalarString()
	case "width":
		_, err := strconv.Atoi(scalar.ScalarString())
		if err != nil {
			c.errorf(scalar, "non-integer width %#v: %s", scalar.ScalarString(), err)
			return
		}
		attrs.Width = &d2graph.Scalar{}
		attrs.Width.Value = scalar.ScalarString()
		attrs.Width.MapKey = f.LastPrimaryKey()
	case "height":
		_, err := strconv.Atoi(scalar.ScalarString())
		if err != nil {
			c.errorf(scalar, "non-integer height %#v: %s", scalar.ScalarString(), err)
			return
		}
		attrs.Height = &d2graph.Scalar{}
		attrs.Height.Value = scalar.ScalarString()
		attrs.Height.MapKey = f.LastPrimaryKey()
	case "link":
		attrs.Link = scalar.ScalarString()
	case "direction":
		dirs := []string{"up", "down", "right", "left"}
		if !go2.Contains(dirs, scalar.ScalarString()) {
			c.errorf(scalar, `direction must be one of %v, got %q`, strings.Join(dirs, ", "), scalar.ScalarString())
			return
		}
		attrs.Direction.Value = scalar.ScalarString()
		attrs.Direction.MapKey = f.LastPrimaryKey()
	case "constraint":
		if _, ok := scalar.(d2ast.String); !ok {
			c.errorf(f.LastPrimaryKey(), "constraint value must be a string")
			return
		}
		attrs.Constraint.Value = scalar.ScalarString()
		attrs.Constraint.MapKey = f.LastPrimaryKey()
	}
}

func (c *compiler) compileStyle(attrs *d2graph.Attributes, m *d2ir.Map) {
	for _, f := range m.Fields {
		c.compileStyleField(attrs, f)
	}
}

func (c *compiler) compileStyleField(attrs *d2graph.Attributes, f *d2ir.Field) {
	if f.Primary() == nil {
		return
	}
	compileStyleFieldInit(attrs, f)
	scalar := f.Primary().Value
	err := attrs.Style.Apply(f.Name, scalar.ScalarString())
	if err != nil {
		c.errorf(scalar, err.Error())
		return
	}
}

func compileStyleFieldInit(attrs *d2graph.Attributes, f *d2ir.Field) {
	switch f.Name {
	case "opacity":
		attrs.Style.Opacity = &d2graph.Scalar{MapKey: f.LastPrimaryKey()}
	case "stroke":
		attrs.Style.Stroke = &d2graph.Scalar{MapKey: f.LastPrimaryKey()}
	case "fill":
		attrs.Style.Fill = &d2graph.Scalar{MapKey: f.LastPrimaryKey()}
	case "stroke-width":
		attrs.Style.StrokeWidth = &d2graph.Scalar{MapKey: f.LastPrimaryKey()}
	case "stroke-dash":
		attrs.Style.StrokeDash = &d2graph.Scalar{MapKey: f.LastPrimaryKey()}
	case "border-radius":
		attrs.Style.BorderRadius = &d2graph.Scalar{MapKey: f.LastPrimaryKey()}
	case "shadow":
		attrs.Style.Shadow = &d2graph.Scalar{MapKey: f.LastPrimaryKey()}
	case "3d":
		attrs.Style.ThreeDee = &d2graph.Scalar{MapKey: f.LastPrimaryKey()}
	case "multiple":
		attrs.Style.Multiple = &d2graph.Scalar{MapKey: f.LastPrimaryKey()}
	case "font":
		attrs.Style.Font = &d2graph.Scalar{MapKey: f.LastPrimaryKey()}
	case "font-size":
		attrs.Style.FontSize = &d2graph.Scalar{MapKey: f.LastPrimaryKey()}
	case "font-color":
		attrs.Style.FontColor = &d2graph.Scalar{MapKey: f.LastPrimaryKey()}
	case "animated":
		attrs.Style.Animated = &d2graph.Scalar{MapKey: f.LastPrimaryKey()}
	case "bold":
		attrs.Style.Bold = &d2graph.Scalar{MapKey: f.LastPrimaryKey()}
	case "italic":
		attrs.Style.Italic = &d2graph.Scalar{MapKey: f.LastPrimaryKey()}
	case "underline":
		attrs.Style.Underline = &d2graph.Scalar{MapKey: f.LastPrimaryKey()}
	case "filled":
		attrs.Style.Filled = &d2graph.Scalar{MapKey: f.LastPrimaryKey()}
	case "width":
		attrs.Width = &d2graph.Scalar{MapKey: f.LastPrimaryKey()}
	case "height":
		attrs.Height = &d2graph.Scalar{MapKey: f.LastPrimaryKey()}
	}
}

func (c *compiler) compileEdge(obj *d2graph.Object, e *d2ir.Edge) {
	edge, err := obj.Connect(d2graphIDA(e.ID.SrcPath), d2graphIDA(e.ID.DstPath), e.ID.SrcArrow, e.ID.DstArrow, "")
	if err != nil {
		c.errorf(e.References[0].AST(), err.Error())
		return
	}

	if e.Primary() != nil {
		c.compileLabel(edge.Attributes, e)
	}
	if e.Map() != nil {
		for _, f := range e.Map().Fields {
			_, ok := d2graph.ReservedKeywords[f.Name]
			if !ok {
				c.errorf(f.References[0].AST(), `edge map keys must be reserved keywords`)
				continue
			}
			c.compileEdgeField(edge, f)
		}
	}

	for _, er := range e.References {
		scopeObjIDA := d2ir.IDA(er.Context.ScopeMap)
		scopeObj, _ := edge.Src.Graph.Root.HasChild(scopeObjIDA)
		edge.References = append(edge.References, d2graph.EdgeReference{
			Edge:            er.Context.Edge,
			MapKey:          er.Context.Key,
			MapKeyEdgeIndex: er.Context.EdgeIndex(),
			Scope:           er.Context.Scope,
			ScopeObj:        scopeObj,
		})
	}
}

func (c *compiler) compileEdgeField(edge *d2graph.Edge, f *d2ir.Field) {
	keyword := strings.ToLower(f.Name)
	_, isReserved := d2graph.SimpleReservedKeywords[keyword]
	if isReserved {
		c.compileReserved(edge.Attributes, f)
		return
	} else if f.Name == "style" {
		if f.Map() == nil {
			return
		}
		c.compileStyle(edge.Attributes, f.Map())
		return
	}

	if f.Name == "source-arrowhead" || f.Name == "target-arrowhead" {
		if f.Map() != nil {
			c.compileArrowheads(edge, f)
		}
	}
}

func (c *compiler) compileArrowheads(edge *d2graph.Edge, f *d2ir.Field) {
	var attrs *d2graph.Attributes
	if f.Name == "source-arrowhead" {
		edge.SrcArrowhead = &d2graph.Attributes{}
		attrs = edge.SrcArrowhead
	} else {
		edge.DstArrowhead = &d2graph.Attributes{}
		attrs = edge.DstArrowhead
	}

	if f.Primary() != nil {
		c.compileLabel(attrs, f)
	}

	for _, f2 := range f.Map().Fields {
		keyword := strings.ToLower(f2.Name)
		_, isReserved := d2graph.SimpleReservedKeywords[keyword]
		if isReserved {
			c.compileReserved(attrs, f2)
			continue
		} else if f2.Name == "style" {
			if f2.Map() == nil {
				continue
			}
			c.compileStyle(attrs, f2.Map())
			continue
		} else {
			c.errorf(f2.LastRef().AST(), `source-arrowhead/target-arrowhead map keys must be reserved keywords`)
			continue
		}
	}
}

// TODO add more, e.g. C, bash
var ShortToFullLanguageAliases = map[string]string{
	"md":  "markdown",
	"tex": "latex",
	"js":  "javascript",
	"go":  "golang",
	"py":  "python",
	"rb":  "ruby",
	"ts":  "typescript",
}
var FullToShortLanguageAliases map[string]string

func (c *compiler) compileClass(obj *d2graph.Object) {
	obj.Class = &d2target.Class{}
	for _, f := range obj.ChildrenArray {
		visiblity := "public"
		name := f.IDVal
		// See https://www.uml-diagrams.org/visibility.html
		if name != "" {
			switch name[0] {
			case '+':
				name = name[1:]
			case '-':
				visiblity = "private"
				name = name[1:]
			case '#':
				visiblity = "protected"
				name = name[1:]
			}
		}

		if !strings.Contains(f.IDVal, "(") {
			typ := f.Attributes.Label.Value
			if typ == f.IDVal {
				typ = ""
			}
			obj.Class.Fields = append(obj.Class.Fields, d2target.ClassField{
				Name:       name,
				Type:       typ,
				Visibility: visiblity,
			})
		} else {
			// TODO: Not great, AST should easily allow specifying alternate primary field
			// as an explicit label should change the name.
			returnType := f.Attributes.Label.Value
			if returnType == f.IDVal {
				returnType = "void"
			}
			obj.Class.Methods = append(obj.Class.Methods, d2target.ClassMethod{
				Name:       name,
				Return:     returnType,
				Visibility: visiblity,
			})
		}
	}

	for _, ch := range obj.ChildrenArray {
		for i := 0; i < len(obj.Graph.Objects); i++ {
			if obj.Graph.Objects[i] == ch {
				obj.Graph.Objects = append(obj.Graph.Objects[:i], obj.Graph.Objects[i+1:]...)
				i--
			}
		}
	}
	obj.Children = nil
	obj.ChildrenArray = nil
}

func (c *compiler) compileSQLTable(obj *d2graph.Object) {
	obj.SQLTable = &d2target.SQLTable{}
	for _, col := range obj.ChildrenArray {
		typ := col.Attributes.Label.Value
		if typ == col.IDVal {
			// Not great, AST should easily allow specifying alternate primary field
			// as an explicit label should change the name.
			typ = ""
		}
		d2Col := d2target.SQLColumn{
			Name: d2target.Text{Label: col.IDVal},
			Type: d2target.Text{Label: typ},
		}
		if col.Attributes.Constraint.Value != "" {
			d2Col.Constraint = col.Attributes.Constraint.Value
		}
		obj.SQLTable.Columns = append(obj.SQLTable.Columns, d2Col)
	}

	for _, ch := range obj.ChildrenArray {
		for i := 0; i < len(obj.Graph.Objects); i++ {
			if obj.Graph.Objects[i] == ch {
				obj.Graph.Objects = append(obj.Graph.Objects[:i], obj.Graph.Objects[i+1:]...)
				i--
			}
		}
	}
	obj.Children = nil
	obj.ChildrenArray = nil
}

func (c *compiler) validateKeys(obj *d2graph.Object, m *d2ir.Map) {
	for _, f := range m.Fields {
		c.validateKey(obj, f)
	}
}

func (c *compiler) validateKey(obj *d2graph.Object, f *d2ir.Field) {
	keyword := strings.ToLower(f.Name)
	_, isReserved := d2graph.SimpleReservedKeywords[keyword]
	if isReserved {
		switch obj.Attributes.Shape.Value {
		case d2target.ShapeSQLTable, d2target.ShapeClass:
		default:
			if len(obj.Children) > 0 && (f.Name == "width" || f.Name == "height") {
				c.errorf(f.LastPrimaryKey(), mk.Range.End, fmt.Sprintf("%s cannot be used on container: %s", f.Name, obj.AbsID()))
			}
		}

		switch obj.Attributes.Shape.Value {
		case d2target.ShapeCircle, d2target.ShapeSquare:
			checkEqual := (reserved == "width" && obj.Attributes.Height != nil) || (reserved == "height" && obj.Attributes.Width != nil)
			if checkEqual && obj.Attributes.Width.Value != obj.Attributes.Height.Value {
				c.errorf(f.LastPrimaryKey(), "width and height must be equal for %s shapes", obj.Attributes.Shape.Value)
			}
		}

		switch f.Name {
		case "width":
			if obj.Attributes.Shape.Value != d2target.ShapeImage {
				c.errorf(f.LastPrimaryKey(), "width is only applicable to image shapes.")
			}
		case "height":
			if obj.Attributes.Shape.Value != d2target.ShapeImage {
				c.errorf(f.LastPrimaryKey(), "height is only applicable to image shapes.")
			}
		case "shape":
			switch obj.Attributes.Shape.Value {
			case d2target.ShapeSQLTable, d2target.ShapeClass:
			case d2target.ShapeImage:
				if obj.Attributes.Icon == nil {
					c.errorf(f.LastPrimaryKey(), `image shape must include an "icon" field`)
				}
			default:
				if len(obj.Children) > 0 && (f.Name == "width" || f.Name == "height") {
					c.errorf(f.LastPrimaryKey(), fmt.Sprintf("%s cannot be used on container: %s", f.Name, obj.AbsID()))
				}
			}

			in := d2target.IsShape(obj.Attributes.Shape.Value)
			_, arrowheadIn := d2target.Arrowheads[obj.Attributes.Shape.Value]
			if !in && arrowheadIn {
				c.errorf(f.LastPrimaryKey(), fmt.Sprintf(`invalid shape, can only set "%s" for arrowheads`, obj.Attributes.Shape.Value))
			}
		}
		return
	}

	if obj.Attributes.Style.ThreeDee != nil {
		if !strings.EqualFold(obj.Attributes.Shape.Value, d2target.ShapeSquare) && !strings.EqualFold(obj.Attributes.Shape.Value, d2target.ShapeRectangle) {
			c.errorf(obj.Attributes.Style.ThreeDee.MapKey, `key "3d" can only be applied to squares and rectangles`)
		}
	}

	if obj.Attributes.Shape.Value == d2target.ShapeImage {
		c.errorf(f.LastRef().AST(), "image shapes cannot have children.")
		return
	}

	obj, ok := obj.HasChild([]string{f.Name})
	if ok && f.Map() != nil {
		c.validateKeys(obj, f.Map())
	}
}

func (c *compiler) validateNear(g *d2graph.Graph) {
	for _, obj := range g.Objects {
		if obj.Attributes.NearKey != nil {
			_, isKey := g.Root.HasChild(d2graph.Key(obj.Attributes.NearKey))
			_, isConst := d2graph.NearConstants[d2graph.Key(obj.Attributes.NearKey)[0]]
			if !isKey && !isConst {
				c.errorf(obj.Attributes.NearKey, "near key %#v must be the absolute path to a shape or one of the following constants: %s", d2format.Format(obj.Attributes.NearKey), strings.Join(d2graph.NearConstantsArray, ", "))
				continue
			}
			if !isKey && isConst && obj.Parent != g.Root {
				c.errorf(obj.Attributes.NearKey, "constant near keys can only be set on root level shapes")
				continue
			}
			if !isKey && isConst && len(obj.ChildrenArray) > 0 {
				c.errorf(obj.Attributes.NearKey, "constant near keys cannot be set on shapes with children")
				continue
			}
			if !isKey && isConst {
				is := false
				for _, e := range g.Edges {
					if e.Src == obj || e.Dst == obj {
						is = true
						break
					}
				}
				if is {
					c.errorf(obj.Attributes.NearKey, "constant near keys cannot be set on connected shapes")
					continue
				}
			}
		}
	}
}

func init() {
	FullToShortLanguageAliases = make(map[string]string, len(ShortToFullLanguageAliases))
	for k, v := range ShortToFullLanguageAliases {
		FullToShortLanguageAliases[v] = k
	}
}

func d2graphIDA(irIDA []string) (ida []string) {
	for _, el := range irIDA {
		n := &d2ast.KeyPath{
			Path: []*d2ast.StringBox{d2ast.MakeValueBox(d2ast.RawString(el, true)).StringBox()},
		}
		ida = append(ida, d2format.Format(n))
	}
	return ida
}
