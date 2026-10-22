package shader

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
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
			b, _ := json.Marshal(values[p.Key])
			for j, opt := range p.OptionValues() {
				if string(b) == string(opt) {
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
func propertyDecls(defs []wpk.Prop, target Target) (fields, slots, init string, err error) {
	for i, p := range defs {
		t, e := propertyType(p)
		if e != nil {
			return "", "", "", e
		}
		if (p.Key == "iResolution" || p.Key == "iTime" || p.Key == "iTimeDelta" || p.Key == "iFrame" || p.Key == "iMouse" || p.Key == "iDate" || p.Key == "iAudio" || p.Key == "iAudioLevel") || strings.HasPrefix(p.Key, "wp_") || isType(p.Key) {
			return "", "", "", fmt.Errorf("shader line 1: reserved uniform name %s", p.Key)
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
			fields += "#define " + p.Key + " " + access + "\n"
		} else {
			fields += typ + " " + p.Key + ";\n"
			access = strings.ReplaceAll(access, n, "u."+n)
			init += "s." + p.Key + "=" + access + ";\n"
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
