//go:build windows

package termcolor

import (
	"os"

	"golang.org/x/sys/windows"
)

// EnableWindowsANSI 打开 Windows 控制台的 VT 处理，使 ANSI 颜色生效。
func EnableWindowsANSI() {
	enableVT(windows.Stdout)
	enableVT(windows.Stderr)
}

func enableVT(handle windows.Handle) {
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return
	}
	mode |= windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING
	_ = windows.SetConsoleMode(handle, mode)
	_ = os.Setenv("PIM_ANSI", "1")
}
