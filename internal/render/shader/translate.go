package shader

import (
	"fmt"
	"strconv"
	"strings"
	"wpapp/internal/wpk"
)

type Target string

const (
	HLSL Target = "hlsl"
	MSL  Target = "msl"
)

type token struct {
	text string
	line int
}
type expr struct{ code, typ string }
type translator struct {
	ts               []token
	at               int
	target           Target
	vars             map[string]string
	funcs            map[string]string
	main             bool
	initializers     []string
	constructors     []string
	constructorNames map[string]string
	depth            int
}

func lex(src string) ([]token, error) {
	var out []token
	line := 1
	ident := func(c byte) bool { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
	digit := func(c byte) bool { return c >= '0' && c <= '9' }
	for i := 0; i < len(src); {
		c := src[i]
		if c == '\n' {
			line++
			i++
			continue
		}
		if c == ' ' || c == '\r' || c == '\t' {
			i++
			continue
		}
		if i+1 < len(src) && src[i:i+2] == "//" {
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		}
		if i+1 < len(src) && src[i:i+2] == "/*" {
			i += 2
			closed := false
			for i+1 < len(src) {
				if src[i:i+2] == "*/" {
					i += 2
					closed = true
					break
				}
				if src[i] == '\n' {
					line++
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("shader line %d: unclosed comment", line)
			}
			continue
		}
		start := i
		i++
		if ident(c) {
			for i < len(src) && (ident(src[i]) || digit(src[i])) {
				i++
			}
		} else if digit(c) || c == '.' && i < len(src) && digit(src[i]) {
			for i < len(src) && (digit(src[i]) || src[i] == '.') {
				i++
			}
			if i < len(src) && (src[i] == 'e' || src[i] == 'E') {
				i++
				if i < len(src) && (src[i] == '+' || src[i] == '-') {
					i++
				}
				for i < len(src) && digit(src[i]) {
					i++
				}
			}
		} else if !strings.ContainsRune("(){}[];,.+-*/%=!<>?:&|", rune(c)) {
			return nil, fmt.Errorf("shader line %d: unsupported token %q", line, c)
		}
		if i == start+1 && i < len(src) && strings.Contains(" == != <= >= ++ -- += -= *= /= && || ", " "+src[start:i+1]+" ") {
			i++
		}
		out = append(out, token{src[start:i], line})
		if len(out) > 100000 {
			return nil, fmt.Errorf("shader line %d: source too complex", line)
		}
	}
	return append(out, token{"", line}), nil
}
func (p *translator) peek() string { return p.ts[p.at].text }
func (p *translator) pop() string {
	x := p.peek()
	if p.at < len(p.ts)-1 {
		p.at++
	}
	return x
}
func (p *translator) fail(msg string) { panic(fmt.Errorf("shader line %d: %s", p.ts[p.at].line, msg)) }
func (p *translator) want(x string) {
	if p.pop() != x {
		p.fail("expected " + x)
	}
}
func (p *translator) take(x string) bool {
	if p.peek() == x {
		p.pop()
		return true
	}
	return false
}
func isType(s string) bool {
	switch s {
	case "void", "float", "int", "bool", "vec2", "vec3", "vec4", "ivec2", "ivec3", "ivec4", "bvec2", "bvec3", "bvec4", "mat2", "mat3", "mat4":
		return true
	}
	return false
}
func (p *translator) typ(s string) string {
	if strings.HasPrefix(s, "vec") {
		return "float" + s[3:]
	}
	if strings.HasPrefix(s, "ivec") {
		return "int" + s[4:]
	}
	if strings.HasPrefix(s, "bvec") {
		return "bool" + s[4:]
	}
	if strings.HasPrefix(s, "mat") {
		return "float" + s[3:] + "x" + s[3:]
	}
	return s
}
func matrix(s string) bool { return strings.HasPrefix(s, "mat") }
func (p *translator) name() string {
	s := p.pop()
	if s == "" || !(s[0] == '_' || s[0] >= 'a' && s[0] <= 'z' || s[0] >= 'A' && s[0] <= 'Z') {
		p.fail("expected identifier")
	}
	if strings.HasPrefix(s, "wp_") {
		p.fail("wp_ prefix is reserved")
	}
	return s
}

// Translate deliberately parses expressions before lowering them: replacing
// type names alone cannot preserve GLSL matrix multiplication or constructors.
func Translate(src string, target Target) (string, error) { return translate(src, target, nil) }
func translate(src string, target Target, defs []wpk.Prop) (result string, err error) {
	defer func() {
		if e := recover(); e != nil {
			if x, ok := e.(error); ok {
				err = x
				result = ""
			} else {
				panic(e)
			}
		}
	}()
	if len(src) > 1<<20 {
		return "", fmt.Errorf("shader line 1: source exceeds 1 MiB")
	}
	if target != HLSL && target != MSL {
		return "", fmt.Errorf("shader line 1: unknown target")
	}
	src, err = preprocess(src)
	if err != nil {
		return "", err
	}
	ts, err := lex(src)
	if err != nil {
		return "", err
	}
	p := translator{ts: ts, target: target, vars: map[string]string{"iResolution": "vec3", "iTime": "float", "iTimeDelta": "float", "iFrame": "int", "iMouse": "vec4", "iDate": "vec4", "iAudioLevel": "float"}, funcs: map[string]string{}}
	fields, slots, initial, e := propertyDecls(defs, target)
	if e != nil {
		return "", e
	}
	for _, d := range defs {
		t, _ := propertyType(d)
		p.vars[d.Key] = t
	}
	p.vars["iAudio"] = "sampler2D"
	var body strings.Builder
	for p.peek() != "" {
		body.WriteString(p.declaration(true))
		body.WriteByte('\n')
	}
	if !p.main {
		return "", fmt.Errorf("shader line 1: void mainImage(out vec4, in vec2) is required")
	}
	pre := helpers(target) + strings.Join(p.constructors, "\n")
	if target == MSL {
		return pre + "struct WPShader {\nfloat3 iResolution; float iTime; float iTimeDelta; int iFrame; float4 iMouse; float4 iDate; float iAudioLevel;\n" + fields + body.String() + "void wp_init() {" + strings.Join(p.initializers, ";") + ";}\n};\n" + strings.Replace(strings.Replace(metalEntry, "float4 wp_audio[128];", "float4 wp_audio[128];"+slots, 1), "WPShader s;", "WPShader s;"+initial, 1), nil
	}
	return pre + fields + "cbuffer WPUniforms : register(b0) { float4 wp_resolutionTime; float4 wp_deltaFrame; float4 iMouse; float4 iDate; float4 wp_audio[128]; " + slots + "};\n#define iResolution wp_resolutionTime.xyz\n#define iTime wp_resolutionTime.w\n#define iTimeDelta wp_deltaFrame.x\n#define iFrame asint(wp_deltaFrame.y)\n#define iAudioLevel wp_deltaFrame.z\n" + body.String() + "void wp_init() {" + strings.Join(p.initializers, ";") + ";}\n" + hlslEntry, nil
}
func (p *translator) declaration(global bool) string {
	if p.take("precision") {
		p.pop()
		p.pop()
		p.want(";")
		return ""
	}
	constant := p.take("const")
	for p.peek() == "highp" || p.peek() == "mediump" || p.peek() == "lowp" {
		p.pop()
	}
	typ := p.pop()
	if !isType(typ) {
		p.fail("unsupported declaration " + typ)
	}
	name := p.name()
	if p.take("(") {
		if !global {
			p.fail("nested function")
		}
		p.funcs[name] = typ
		saved := make(map[string]string, len(p.vars))
		for k, v := range p.vars {
			saved[k] = v
		}
		var params []string
		var signature []string
		for p.peek() != ")" {
			qual := ""
			p.take("const")
			if p.peek() == "in" || p.peek() == "out" || p.peek() == "inout" {
				qual = p.pop()
			}
			for p.peek() == "const" || p.peek() == "highp" || p.peek() == "mediump" || p.peek() == "lowp" {
				p.pop()
			}
			t := p.pop()
			if !isType(t) || t == "void" {
				p.fail("unsupported parameter type " + t)
			}
			n := p.name()
			p.vars[n] = t
			signature = append(signature, qual+":"+t)
			decl := p.typ(t) + " " + n
			if p.target == MSL && (qual == "out" || qual == "inout") {
				decl = "thread " + p.typ(t) + " &" + n
			} else if p.target == HLSL && qual != "" {
				decl = qual + " " + decl
			}
			params = append(params, decl)
			if !p.take(",") {
				break
			}
		}
		p.want(")")
		if p.take(";") {
			p.vars = saved
			return p.typ(typ) + " " + name + "(" + strings.Join(params, ", ") + ");"
		}
		if name == "mainImage" {
			if typ != "void" || len(signature) != 2 || signature[0] != "out:vec4" || (signature[1] != "in:vec2" && signature[1] != ":vec2") {
				p.fail("mainImage must take out vec4 and in vec2")
			}
			p.main = true
		}
		b := p.statement()
		p.vars = saved
		return p.typ(typ) + " " + name + "(" + strings.Join(params, ", ") + ") " + b
	}
	var parts []string
	for {
		p.vars[name] = typ
		decl := name
		if p.take("[") {
			p.fail("arrays are not supported in this subset")
		}
		if p.take("=") {
			value := p.expression(1).code
			if global && !constant {
				p.initializers = append(p.initializers, name+" = "+value)
			} else {
				decl += " = " + value
			}
		}
		parts = append(parts, decl)
		if !p.take(",") {
			break
		}
		name = p.name()
	}
	p.want(";")
	prefix := ""
	if constant {
		prefix = "const "
	}
	if global && p.target == HLSL {
		prefix = "static " + prefix
	}
	return prefix + p.typ(typ) + " " + strings.Join(parts, ", ") + ";"
}
func (p *translator) statement() string {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > 128 {
		p.fail("statement nesting limit exceeded")
	}
	switch p.peek() {
	case "{":
		saved := p.scope()
		defer func() { p.vars = saved }()
		p.pop()
		var b strings.Builder
		b.WriteString("{\n")
		for p.peek() != "}" {
			if p.peek() == "" {
				p.fail("unclosed block")
			}
			b.WriteString(p.statement())
			b.WriteByte('\n')
		}
		p.pop()
		b.WriteString("}")
		return b.String()
	case "return":
		p.pop()
		if p.take(";") {
			return "return;"
		}
		x := p.expression(1)
		p.want(";")
		return "return " + x.code + ";"
	case "if", "while":
		k := p.pop()
		p.want("(")
		x := p.expression(1)
		p.want(")")
		b := k + " (" + x.code + ") " + p.statement()
		if k == "if" && p.take("else") {
			b += " else " + p.statement()
		}
		return b
	case "for":
		saved := p.scope()
		defer func() { p.vars = saved }()
		p.pop()
		p.want("(")
		a := p.statement()
		b := ""
		if p.peek() != ";" {
			b = p.expression(1).code
		}
		p.want(";")
		c := ""
		if p.peek() != ")" {
			c = p.expression(1).code
		}
		p.want(")")
		return "for (" + a + " " + b + "; " + c + ") " + p.statement()
	case "break", "continue", "discard":
		k := p.pop()
		p.want(";")
		if k == "discard" && p.target == MSL {
			return "discard_fragment();"
		}
		return k + ";"
	case ";":
		p.pop()
		return ";"
	}
	if isType(p.peek()) || p.peek() == "const" || p.peek() == "highp" || p.peek() == "mediump" || p.peek() == "lowp" {
		return p.declaration(false)
	}
	x := p.expression(1)
	p.want(";")
	return x.code + ";"
}

var precedence = map[string]int{"=": 1, "+=": 1, "-=": 1, "*=": 1, "/=": 1, "?": 2, "||": 3, "&&": 4, "==": 5, "!=": 5, "<": 6, ">": 6, "<=": 6, ">=": 6, "+": 7, "-": 7, "*": 8, "/": 8, "%": 8}

func (p *translator) expression(min int) expr {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > 128 {
		p.fail("expression nesting limit exceeded")
	}
	left := p.atom()
	operations := 0
	for {
		operations++
		if operations > 512 || len(left.code) > 65536 {
			p.fail("expression complexity limit exceeded")
		}
		op := p.peek()
		prec := precedence[op]
		if prec < min {
			break
		}
		p.pop()
		if op == "?" {
			a := p.expression(1)
			p.want(":")
			b := p.expression(2)
			left = expr{"(" + left.code + " ? " + a.code + " : " + b.code + ")", a.typ}
			continue
		}
		next := prec + 1
		if prec == 1 {
			next = prec
		}
		r := p.expression(next)
		typ := left.typ
		if typ == "float" || typ == "int" {
			typ = r.typ
		}
		if matrix(left.typ) && strings.HasPrefix(r.typ, "vec") {
			typ = r.typ
		}
		if strings.HasPrefix(left.typ, "vec") && matrix(r.typ) {
			typ = left.typ
		}
		code := "(" + left.code + " " + op + " " + r.code + ")"
		if op == "*" && p.target == HLSL && (matrix(left.typ) || matrix(r.typ)) {
			code = "mul(" + left.code + ", " + r.code + ")"
		}
		if prec == 1 && strings.Contains(left.code, "transpose(") {
			p.fail("matrix column writes are unsupported")
		}
		if op == "*=" && p.target == HLSL && (matrix(left.typ) || matrix(r.typ)) {
			code = "(" + left.code + " = mul(" + left.code + ", " + r.code + "))"
		}
		left = expr{code, typ}
	}
	return left
}
func (p *translator) atom() expr {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > 128 {
		p.fail("expression nesting limit exceeded")
	}
	var x expr
	s := p.pop()
	if s == "" {
		p.fail("expected expression")
	}
	switch s {
	case "(":
		x = p.expression(1)
		p.want(")")
		x.code = "(" + x.code + ")"
	case "+", "-", "!", "++", "--":
		x = p.atom()
		x.code = s + x.code
	default:
		if s[0] >= '0' && s[0] <= '9' || s[0] == '.' && len(s) > 1 {
			typ := "int"
			if strings.ContainsAny(s, ".eE") {
				typ = "float"
			}
			if p.peek() == "f" || p.peek() == "F" {
				p.pop()
				typ = "float"
			}
			x = expr{s, typ}
		} else if s == "true" || s == "false" {
			x = expr{s, "bool"}
		} else if p.take("(") {
			var args []expr
			for p.peek() != ")" {
				args = append(args, p.expression(1))
				if !p.take(",") {
					break
				}
			}
			p.want(")")
			x = p.call(s, args)
		} else {
			t, ok := p.vars[s]
			if !ok {
				p.fail("unknown identifier " + s)
			}
			x = expr{s, t}
		}
	}
	for {
		if p.take(".") {
			sw := p.name()
			if len(sw) > 4 || !strings.Contains(x.typ, "vec") {
				p.fail("invalid vector swizzle " + sw)
			}
			group := ""
			for _, candidate := range []string{"xyzw", "rgba", "stpq"} {
				if strings.ContainsRune(candidate, rune(sw[0])) {
					group = candidate
					break
				}
			}
			if group == "" {
				p.fail("invalid swizzle " + sw)
			}
			canonical := ""
			dim := int(x.typ[len(x.typ)-1] - '0')
			for _, c := range sw {
				index := strings.IndexRune(group, c)
				if index < 0 || index >= dim {
					p.fail("invalid swizzle " + sw)
				}
				canonical += string("xyzw"[index])
			}
			sw = canonical
			x.code += "." + sw
			base, prefix := "float", "vec"
			if strings.HasPrefix(x.typ, "ivec") {
				base, prefix = "int", "ivec"
			}
			if strings.HasPrefix(x.typ, "bvec") {
				base, prefix = "bool", "bvec"
			}
			x.typ = base
			if len(sw) > 1 {
				x.typ = prefix + strconv.Itoa(len(sw))
			}
		} else if p.take("[") {
			i := p.expression(1)
			p.want("]")
			if matrix(x.typ) && p.target == HLSL {
				x.code = "transpose(" + x.code + ")[" + i.code + "]"
			} else {
				x.code += "[" + i.code + "]"
			}
			if matrix(x.typ) {
				x.typ = "vec" + x.typ[3:]
			} else {
				x.typ = "float"
			}
		} else if p.peek() == "++" || p.peek() == "--" {
			x.code += p.pop()
		} else {
			break
		}
	}
	return x
}
func (p *translator) call(name string, a []expr) expr {
	codes := make([]string, len(a))
	for i, x := range a {
		codes[i] = x.code
	}
	args := strings.Join(codes, ", ")
	if name == "texture" && len(a) == 2 && a[0].code == "iAudio" {
		return expr{"float4(0.0, 0.0, 0.0, 0.0)", "vec4"}
	}
	if isType(name) {
		if len(a) == 0 {
			p.fail("empty constructor")
		}
		n := p.typ(name)
		if matrix(name) {
			return p.matrixConstructor(name, a)
		}
		if strings.Contains(name, "vec") && len(a) == 1 && strings.Contains(a[0].typ, "vec") {
			want := int(name[len(name)-1] - '0')
			have := int(a[0].typ[len(a[0].typ)-1] - '0')
			if have < want {
				p.fail("vector constructor has too few components")
			}
			if have > want {
				return expr{n + "((" + args + ")." + "xyzw"[:want] + ")", name}
			}
		}
		if p.target == HLSL && len(a) == 1 && strings.Contains(name, "vec") && !strings.Contains(a[0].typ, "vec") {
			return expr{"(" + strings.TrimRight(p.typ(name), "234") + "(" + args + "))." + strings.Repeat("x", int(name[len(name)-1]-'0')), name}
		}
		return expr{n + "(" + args + ")", name}
	}
	if t, ok := p.funcs[name]; ok {
		return expr{name + "(" + args + ")", t}
	}
	allowed := " sin cos tan asin acos atan sinh cosh tanh abs sign floor ceil round trunc fract mod min max clamp mix step smoothstep length distance dot cross normalize reflect refract pow exp exp2 log log2 sqrt inversesqrt radians degrees fract fwidth dFdx dFdy any all "
	if !strings.Contains(allowed, " "+name+" ") {
		p.fail("unsupported function " + name)
	}
	if len(a) == 0 {
		p.fail("missing function arguments")
	}
	typ := a[0].typ
	switch name {
	case "length", "distance", "dot":
		typ = "float"
	case "any", "all":
		typ = "bool"
	case "atan":
		if len(a) == 2 {
			name = "atan2"
		}
	case "mod":
		if len(a) != 2 {
			p.fail("mod requires two arguments")
		}
		return p.modulo(a)
	case "inversesqrt":
		name = "rsqrt"
	}
	if p.target == HLSL {
		switch name {
		case "mix":
			name = "lerp"
		case "fract":
			name = "frac"
		case "dFdx":
			name = "ddx"
		case "dFdy":
			name = "ddy"
		}
	} else {
		switch name {
		case "dFdx":
			name = "dfdx"
		case "dFdy":
			name = "dfdy"
		}
	}
	return expr{name + "(" + args + ")", typ}
}
func helpers(t Target) string {
	var b strings.Builder
	if t == MSL {
		b.WriteString("#include <metal_stdlib>\nusing namespace metal;\n")
	}
	for n := 2; n <= 4; n++ {
		typ := fmt.Sprintf("float%dx%d", n, n)
		if t == MSL {
			fmt.Fprintf(&b, "%s wp_mat%d(float s) { return %s(s); }\n", typ, n, typ)
			continue
		}
		fmt.Fprintf(&b, "%s wp_mat%d(float s) { return %s(", typ, n, typ)
		for i := 0; i < n*n; i++ {
			if i > 0 {
				b.WriteString(", ")
			}
			if i/n == i%n {
				b.WriteString("s")
			} else {
				b.WriteString("0.0")
			}
		}
		b.WriteString("); }\n")
	}
	return b.String()
}

const metalEntry = `struct WPUniforms { float4 resolutionTime; float4 deltaFrame; float4 mouse; float4 date; float4 wp_audio[128]; };
vertex float4 wp_vertex(uint id [[vertex_id]]) { return float4(id == 1 ? 3.0 : -1.0, id == 2 ? 3.0 : -1.0, 0.0, 1.0); }
fragment float4 wp_fragment(float4 pos [[position]], constant WPUniforms &u [[buffer(0)]]) {
 WPShader s; s.iResolution=u.resolutionTime.xyz; s.iTime=u.resolutionTime.w; s.iTimeDelta=u.deltaFrame.x; s.iFrame=as_type<int>(u.deltaFrame.y); s.iMouse=u.mouse; s.iDate=u.date; s.iAudioLevel=u.deltaFrame.z;
 s.wp_init(); float4 color=float4(0.0); s.mainImage(color,float2(pos.x,u.resolutionTime.y-pos.y)); return color;
}
`
const hlslEntry = `
float4 wp_vertex(uint id : SV_VertexID) : SV_Position { return float4(id == 1 ? 3.0 : -1.0, id == 2 ? 3.0 : -1.0, 0.0, 1.0); }
float4 wp_fragment(float4 pos : SV_Position) : SV_Target { float4 color=0.0; wp_init(); mainImage(color,float2(pos.x,iResolution.y-pos.y)); return color; }
`

func (p *translator) scope() map[string]string {
	saved := make(map[string]string, len(p.vars))
	for k, v := range p.vars {
		saved[k] = v
	}
	return saved
}
