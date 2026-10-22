//go:build !windows

package termcolor

// EnableWindowsANSI 在非 Windows 上为空操作（终端通常已支持 ANSI）。
func EnableWindowsANSI() {}
