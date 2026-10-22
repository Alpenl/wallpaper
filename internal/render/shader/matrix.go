package shader

import (
	"fmt"
	"strconv"
	"strings"
)

// Constructor helpers evaluate each argument once, then fill columns. This also
// preserves side effects in mixed scalar/vector constructors and matrix resize.
func (p *translator) matrixConstructor(name string, a []expr) expr {
	n, _ := strconv.Atoi(name[3:])
	if len(a) == 1 && (a[0].typ == "float" || a[0].typ == "int") {
		return expr{"wp_" + name + "(" + a[0].code + ")", name}
	}
	signature := name
	var params, codes []string
	for i, x := range a {
		signature += "_" + x.typ
		params = append(params, fmt.Sprintf("%s a%d", p.typ(x.typ), i))
		codes = append(codes, x.code)
	}
	if p.constructorNames == nil {
		p.constructorNames = map[string]string{}
	}
	if helper := p.constructorNames[signature]; helper != "" {
		return expr{helper + "(" + strings.Join(codes, ", ") + ")", name}
	}
	var components []string
	if len(a) == 1 && matrix(a[0].typ) {
		m, _ := strconv.Atoi(a[0].typ[3:])
		for col := 0; col < n; col++ {
			for row := 0; row < n; row++ {
				v := "0.0"
				if col < m && row < m {
					if p.target == HLSL {
						v = fmt.Sprintf("a0[%d][%d]", row, col)
					} else {
						v = fmt.Sprintf("a0[%d][%d]", col, row)
					}
				} else if col == row {
					v = "1.0"
				}
				components = append(components, v)
			}
		}
	} else {
		for i, x := range a {
			if matrix(x.typ) {
				p.fail("matrix constructor cannot mix matrices and other arguments")
			}
			count := 1
			if strings.Contains(x.typ, "vec") {
				count = int(x.typ[len(x.typ)-1] - '0')
			}
			if x.typ == "bool" || strings.HasPrefix(x.typ, "bvec") {
				p.fail("boolean matrix arguments are unsupported")
			}
			for j := 0; j < count; j++ {
				v := fmt.Sprintf("a%d", i)
				if count > 1 {
					v += "." + string("xyzw"[j])
				}
				components = append(components, "float("+v+")")
			}
		}
		if len(components) != n*n {
			p.fail("matrix constructor requires exactly " + strconv.Itoa(n*n) + " components")
		}
	}
	var result string
	if p.target == HLSL {
		result = "transpose(" + p.typ(name) + "(" + strings.Join(components, ", ") + "))"
	} else {
		var cols []string
		for i := 0; i < n; i++ {
			cols = append(cols, fmt.Sprintf("float%d(%s)", n, strings.Join(components[i*n:(i+1)*n], ", ")))
		}
		result = p.typ(name) + "(" + strings.Join(cols, ", ") + ")"
	}
	helper := fmt.Sprintf("wp_constructor%d", len(p.constructors))
	p.constructorNames[signature] = helper
	p.constructors = append(p.constructors, p.typ(name)+" "+helper+"("+strings.Join(params, ", ")+") { return "+result+"; }\n")
	return expr{helper + "(" + strings.Join(codes, ", ") + ")", name}
}

// GLSL mod must evaluate each argument once (including inout function calls).
// Unlike fmod, negative inputs use floor rather than truncation.
func (p *translator) modulo(a []expr) expr {
	left, right := a[0].typ, a[1].typ
	if strings.HasPrefix(left, "ivec") {
		left = left[1:]
	}
	if strings.HasPrefix(right, "ivec") {
		right = right[1:]
	}
	if left == "int" {
		left = "float"
	}
	if right == "int" {
		right = "float"
	}
	result := left
	if result == "float" {
		result = right
	}
	key := "mod_" + left + "_" + right
	if p.constructorNames == nil {
		p.constructorNames = map[string]string{}
	}
	helper := p.constructorNames[key]
	if helper == "" {
		helper = "wp_" + key
		p.constructorNames[key] = helper
		p.constructors = append(p.constructors, fmt.Sprintf("%s %s(%s x, %s y) { return x - y * floor(x / y); }\n", p.typ(result), helper, p.typ(left), p.typ(right)))
	}
	return expr{helper + "(" + a[0].code + ", " + a[1].code + ")", result}
}
