package shader

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
	"wpapp/internal/reactive"
	"wpapp/internal/wpk"
)

// Each field occupies one 16-byte slot, shared by cbuffer and Metal float4.
// Avoid Go struct ABI and bool size dependencies. Audio and mouse are zero
// unless the isolated host has both package consent and global authorization.
const AudioBins = 128
const BaseUniformBytes = 64 + AudioBins*16
const MaxUniformBytes = BaseUniformBytes + 64*16

type Uniforms struct {
	Bytes [MaxUniformBytes]byte
	Size  int
}

func (u *Uniforms) put(slot, component int, value float32) {
	binary.LittleEndian.PutUint32(u.Bytes[slot*16+component*4:], math.Float32bits(value))
}
func (u *Uniforms) Frame(w, h int, seconds, delta float32, frame int32, date time.Time) {
	u.put(0, 0, float32(w))
	u.put(0, 1, float32(h))
	u.put(0, 2, 1)
	u.put(0, 3, seconds)
	u.put(1, 0, delta)
	binary.LittleEndian.PutUint32(u.Bytes[20:], uint32(frame))
	u.put(3, 0, float32(date.Year()))
	u.put(3, 1, float32(date.Month()-1))
	u.put(3, 2, float32(date.Day()))
	u.put(3, 3, float32(date.Hour()*3600+date.Minute()*60+date.Second())+float32(date.Nanosecond())/1e9)
}
func propertyType(p wpk.Prop) (string, error) {
	switch p.Type {
	case "slider":
		return "float", nil
	case "bool":
		return "bool", nil
	case "color":
		return "vec3", nil
	case "select":
		return "int", nil
	}
	return "", fmt.Errorf("shader line 1: unsupported uniform property %s (%s)", p.Key, p.Type)
}

func jsonValueEqual(val any, raw json.RawMessage) bool {
	var optVal any
	if err := json.Unmarshal(raw, &optVal); err != nil {
		return false
	}
	return reflect.DeepEqual(val, optVal)
}

func (u *Uniforms) Properties(defs []wpk.Prop, raw string) error {
	if len(defs) > 64 {
		return fmt.Errorf("shader: too many uniforms")
	}
	values, e := wpk.PropertyValues(defs, raw)
	if e != nil {
		return e
	}
	for i, p := range defs {
		if _, e := propertyType(p); e != nil {
			return e
		}
		slot := BaseUniformBytes/16 + i
		switch p.Type {
		case "slider":
			u.put(slot, 0, float32(values[p.Key].(float64)))
		case "bool":
			v := float32(0)
			if values[p.Key].(bool) {
				v = 1
			}
			u.put(slot, 0, v)
		case "color":
			s := values[p.Key].(string)
			for j := 0; j < 3; j++ {
				v, _ := strconv.ParseUint(s[1+j*2:3+j*2], 16, 8)
				u.put(slot, j, float32(v)/255)
			}
		case "select":
			val := values[p.Key]
			for j, opt := range p.OptionValues() {
				if jsonValueEqual(val, opt) {
					u.put(slot, 0, float32(j))
					break
				}
			}
		}
	}
	u.Size = BaseUniformBytes + 16*len(defs)
	return nil
}
func TranslateProperties(src string, target Target, defs []wpk.Prop) (string, error) {
	return translate(src, target, defs)
}

var reservedTargetKeywords = map[string]bool{
	// ShaderToy/translator reserved names
	"iResolution": true, "iTime": true, "iTimeDelta": true, "iFrame": true,
	"iMouse": true, "iDate": true, "iAudio": true, "iAudioLevel": true,
	"mainImage": true, "WPShader": true, "WPUniforms": true,
	// HLSL keywords and built-in types
	"half": true, "half2": true, "half3": true, "half4": true,
	"half2x2": true, "half3x3": true, "half4x4": true,
	"double": true, "fixed": true, "fixed2": true, "fixed3": true, "fixed4": true,
	"min16float": true, "min10float": true, "min16int": true, "min12int": true, "min16uint": true,
	"dword": true, "snorm": true, "unorm": true, "string": true,
	"matrix": true, "vector": true, "cbuffer": true, "tbuffer": true,
	"register": true, "packoffset": true, "namespace": true, "class": true,
	"struct": true, "interface": true, "template": true, "typedef": true,
	"typename": true, "static": true, "extern": true, "inline": true,
	"noinline": true, "virtual": true, "override": true, "final": true,
	"const_cast": true, "static_cast": true, "dynamic_cast": true, "reinterpret_cast": true,
	"sizeof": true, "alignof": true, "decltype": true, "nullptr": true,
	"auto": true, "signed": true, "unsigned": true, "short": true, "long": true,
	"char": true, "wchar_t": true, "point": true, "line": true, "triangle": true,
	"lineadj": true, "triangleadj": true, "linear": true, "centroid": true,
	"nointerpolation": true, "noperspective": true, "sample": true,
	"groupshared": true, "globallycoherent": true, "row_major": true, "column_major": true,
	"precise": true, "shared": true, "volatile": true, "mutable": true,
	"friend": true, "operator": true, "private": true, "protected": true, "public": true,
	"this": true, "new": true, "delete": true, "try": true, "catch": true, "throw": true,
	"using": true, "enum": true, "union": true, "switch": true, "case": true,
	"default": true, "do": true, "goto": true, "asm": true, "pass": true,
	"technique": true, "technique10": true, "technique11": true,
	"compile": true, "compile_fragment": true, "stateblock": true, "stateblock_state": true,
	"SamplerState": true, "SamplerComparisonState": true,
	"Texture1D": true, "Texture2D": true, "Texture3D": true, "TextureCube": true,
	"RWTexture2D": true, "Buffer": true, "StructuredBuffer": true, "ByteAddressBuffer": true,
	"AppendStructuredBuffer": true, "ConsumeStructuredBuffer": true,
	"InputPatch": true, "OutputPatch": true, "PointStream": true, "LineStream": true, "TriangleStream": true,
	"pixelshader": true, "vertexshader": true, "hullshader": true, "domainshader": true,
	"geometryshader": true, "computeshader": true, "fxgroup": true, "export": true,
	"float1": true, "float2": true, "float3": true, "float4": true,
	"int1": true, "int2": true, "int3": true, "int4": true,
	"uint1": true, "uint2": true, "uint3": true, "uint4": true,
	"bool1": true, "bool2": true, "bool3": true, "bool4": true,
	"float2x2": true, "float3x3": true, "float4x4": true,
	// MSL keywords and built-in types
	"device": true, "constant": true, "thread": true, "threadgroup": true,
	"threadgroup_imageblock": true, "ray_data": true, "object_data": true,
	"vertex": true, "fragment": true, "kernel": true, "metal": true,
	"packed_float2": true, "packed_float3": true, "packed_float4": true,
	"packed_int2": true, "packed_int3": true, "packed_int4": true,
	"packed_uint2": true, "packed_uint3": true, "packed_uint4": true,
	"texture1d": true, "texture2d": true, "texture3d": true, "texturecube": true,
	"sampler": true, "depth2d": true, "discard_fragment": true, "as_type": true,
}

func isReservedUniformName(s string) bool {
	return strings.HasPrefix(s, "wp_") || isType(s) || reservedTargetKeywords[s]
}

func isReservedUserIdent(s string) bool {
	if s == "mainImage" {
		return false
	}
	return strings.HasPrefix(s, "wp_") || reservedTargetKeywords[s]
}

func propertyDecls(defs []wpk.Prop, target Target) (fields, slots, init string, propExprs map[string]string, err error) {
	propExprs = map[string]string{}
	for i, p := range defs {
		t, e := propertyType(p)
		if e != nil {
			return "", "", "", nil, e
		}
		if isReservedUniformName(p.Key) {
			return "", "", "", nil, fmt.Errorf("shader line 1: reserved uniform name %s", p.Key)
		}
		n := fmt.Sprintf("wp_prop%d", i)
		slots += "float4 " + n + ";\n"
		access := n + ".x"
		typ := t
		if t == "vec3" {
			typ = "float3"
			access = n + ".xyz"
		} else if t == "int" {
			access = "int(" + access + ")"
		} else if t == "bool" {
			access = "(" + access + " != 0.0)"
		}
		if target == HLSL {
			propExprs[p.Key] = access
		} else {
			fields += typ + " " + p.Key + ";\n"
			access = strings.ReplaceAll(access, n, "wp_u."+n)
			init += "wp_s." + p.Key + "=" + access + ";\n"
		}
	}
	return
}

func (u *Uniforms) Signals(audio reactive.Spectrum, mouse [4]float32) {
	u.put(1, 2, audio.RMS)
	for i, v := range mouse {
		u.put(2, i, v)
	}
	for i, v := range audio.Bins {
		u.put(4+i, 0, v)
	}
}
