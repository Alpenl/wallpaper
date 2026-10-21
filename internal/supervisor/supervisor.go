// Package supervisor 是 02 章 2.4 的退避规则。崩溃计数、10 分钟 5 次进入 Failed 由 visibility 状态机负责（07 章 7.3）。
// Job Object（2.2）在 platform/win32.SetupJob。
package supervisor

import "time"

// Steps 是重启间隔：0.5、1、2、4、8、16、32、60 s（封顶）。
var Steps = []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second,
	8 * time.Second, 16 * time.Second, 32 * time.Second, 60 * time.Second}

// Backoff 返回第 n 次（从 0 起）重启前的等待时间。
func Backoff(n int) time.Duration {
	if n < 0 {
		n = 0
	}
	if n >= len(Steps) {
		return Steps[len(Steps)-1]
	}
	return Steps[n]
}

// StableAfter：渲染器稳定运行这么久之后，退避计数清零。
const StableAfter = 60 * time.Second
