package mac

import (
	"regexp"
	"strconv"
	"strings"
)

var pctRe = regexp.MustCompile(`(\d+)%`)

// ParsePmset 解析 `pmset -g batt` 输出：（是否电池供电，电量%；-1=未知）。
// 例："Now drawing from 'Battery Power'\n -InternalBattery-0 (id=…)\t57%; discharging; 3:12 remaining present: true"
func ParsePmset(out string) (bool, int) {
	on := strings.Contains(out, "'Battery Power'")
	pct := -1
	if m := pctRe.FindStringSubmatch(out); m != nil {
		pct, _ = strconv.Atoi(m[1])
	}
	return on, pct
}

// Covered 是 50 pt 网格遮挡判定（与 Windows 相同的 Lively 算法，坐标为 CG 左上原点）。
func Covered(disp Rect, wins []Rect) (covered, fullscreen bool) {
	const tile = 50.0
	cols, rows := int((disp.W+tile-1)/tile), int((disp.H+tile-1)/tile)
	if cols <= 0 || rows <= 0 {
		return false, false
	}
	grid := make([]bool, cols*rows)
	cnt := 0
	for _, r := range wins {
		if r.X <= disp.X+2 && r.Y <= disp.Y+2 && r.X+r.W >= disp.X+disp.W-2 && r.Y+r.H >= disp.Y+disp.H-2 {
			fullscreen = true
		}
		x0, y0 := int((r.X-disp.X)/tile), int((r.Y-disp.Y)/tile)
		x1, y1 := int((r.X+r.W-disp.X-1)/tile), int((r.Y+r.H-disp.Y-1)/tile)
		if r.X+r.W <= disp.X || r.Y+r.H <= disp.Y || r.X >= disp.X+disp.W || r.Y >= disp.Y+disp.H {
			continue
		}
		for y := max(0, y0); y <= min(rows-1, y1); y++ {
			for x := max(0, x0); x <= min(cols-1, x1); x++ {
				if !grid[y*cols+x] {
					grid[y*cols+x] = true
					cnt++
				}
			}
		}
	}
	free := len(grid) - cnt
	return fullscreen || free*100 <= len(grid)*5, fullscreen
}

// Rect 是 CG 坐标矩形。
type Rect struct{ X, Y, W, H float64 }

// Debounce3 实现 Wallnetic 策略：连续 3 次采样一致才翻转。
type Debounce3 struct {
	hist  [3]bool
	n     int
	State bool
}

// Push 返回是否翻转。
func (d *Debounce3) Push(v bool) bool {
	d.hist[d.n%3] = v
	d.n++
	if d.n >= 3 && d.hist[0] == d.hist[1] && d.hist[1] == d.hist[2] && d.hist[0] != d.State {
		d.State = d.hist[0]
		return true
	}
	return false
}

// PowerSignals 把 NSProcessInfo 的低电量模式和 thermalState（0 nominal、1 fair、2 serious、3 critical）
// 映射到 07 章的原因：低电量模式 → BatterySaver；serious → ThermalSerious（只限帧 15 fps）；critical → ThermalCritical（暂停）。
func PowerSignals(lowPower bool, thermal int) (saver, serious, critical bool) {
	return lowPower, thermal == 2, thermal >= 3
}

// ThermalName 是 thermalState 的显示名。
func ThermalName(t int) string {
	switch t {
	case 0:
		return "正常"
	case 1:
		return "偏热"
	case 2:
		return "严重"
	case 3:
		return "危急"
	}
	return "未知"
}
