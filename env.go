// env.go - Windows 用户级环境变量持久化
// 通过 syscall 直接调用 Windows API 操作注册表，零外部依赖

package main

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

var (
	modadvapi32         = syscall.NewLazyDLL("advapi32.dll")
	procRegOpenKeyEx    = modadvapi32.NewProc("RegOpenKeyExW")
	procRegSetValueEx   = modadvapi32.NewProc("RegSetValueExW")
	procRegQueryValueEx = modadvapi32.NewProc("RegQueryValueExW")
	procRegDeleteValue  = modadvapi32.NewProc("RegDeleteValueW")
	procRegCloseKey     = modadvapi32.NewProc("RegCloseKey")

	moduser32              = syscall.NewLazyDLL("user32.dll")
	procSendMessageTimeout = moduser32.NewProc("SendMessageTimeoutW")
)

const (
	HKEY_CURRENT_USER    = 0x80000001
	KEY_READ             = 0x20019
	KEY_SET_VALUE        = 0x0002
	REG_SZ               = 1
	HWND_BROADCAST       = 0xFFFF
	WM_SETTINGCHANGE     = 0x001A
	SMTO_ABORTIFHUNG     = 0x0002
	ERROR_FILE_NOT_FOUND = 2
)

// SetUserEnv 设置用户级环境变量（写入注册表，永久生效）
func SetUserEnv(name, value string) error {
	var key syscall.Handle
	envKey, err := syscall.UTF16PtrFromString(`Environment`)
	if err != nil {
		return err
	}

	ret, _, _ := procRegOpenKeyEx.Call(
		HKEY_CURRENT_USER,
		uintptr(unsafe.Pointer(envKey)),
		0,
		KEY_SET_VALUE,
		uintptr(unsafe.Pointer(&key)),
	)
	if ret != 0 {
		return fmt.Errorf("无法打开注册表 HKCU\\Environment")
	}
	defer procRegCloseKey.Call(uintptr(key))

	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}

	valueUTF16 := syscall.StringToUTF16(value)
	ret, _, _ = procRegSetValueEx.Call(
		uintptr(key),
		uintptr(unsafe.Pointer(namePtr)),
		0,
		REG_SZ,
		uintptr(unsafe.Pointer(&valueUTF16[0])),
		uintptr(len(valueUTF16)*2),
	)
	if ret != 0 {
		return fmt.Errorf("无法设置环境变量 %s", name)
	}

	return BroadcastEnvChange()
}

// GetUserEnv 读取用户级环境变量
func GetUserEnv(name string) (string, error) {
	var key syscall.Handle
	envKey, err := syscall.UTF16PtrFromString(`Environment`)
	if err != nil {
		return "", err
	}

	ret, _, _ := procRegOpenKeyEx.Call(
		HKEY_CURRENT_USER,
		uintptr(unsafe.Pointer(envKey)),
		0,
		KEY_READ,
		uintptr(unsafe.Pointer(&key)),
	)
	if ret != 0 {
		return "", fmt.Errorf("无法打开注册表 HKCU\\Environment")
	}
	defer procRegCloseKey.Call(uintptr(key))

	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return "", err
	}

	var bufSize uint32 = 1024
	buf := make([]uint16, bufSize)
	var valType uint32

	ret, _, _ = procRegQueryValueEx.Call(
		uintptr(key),
		uintptr(unsafe.Pointer(namePtr)),
		0,
		uintptr(unsafe.Pointer(&valType)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&bufSize)),
	)
	if ret != 0 {
		if ret == ERROR_FILE_NOT_FOUND {
			return "", fmt.Errorf("环境变量 %s 不存在", name)
		}
		return "", fmt.Errorf("无法读取环境变量 %s", name)
	}

	return syscall.UTF16ToString(buf), nil
}

// DeleteUserEnv 删除用户级环境变量
func DeleteUserEnv(name string) error {
	var key syscall.Handle
	envKey, err := syscall.UTF16PtrFromString(`Environment`)
	if err != nil {
		return err
	}

	ret, _, _ := procRegOpenKeyEx.Call(
		HKEY_CURRENT_USER,
		uintptr(unsafe.Pointer(envKey)),
		0,
		KEY_SET_VALUE,
		uintptr(unsafe.Pointer(&key)),
	)
	if ret != 0 {
		return fmt.Errorf("无法打开注册表 HKCU\\Environment")
	}
	defer procRegCloseKey.Call(uintptr(key))

	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}

	ret, _, _ = procRegDeleteValue.Call(
		uintptr(key),
		uintptr(unsafe.Pointer(namePtr)),
	)
	if ret != 0 {
		return fmt.Errorf("无法删除环境变量 %s", name)
	}

	return BroadcastEnvChange()
}

// BroadcastEnvChange 广播 WM_SETTINGCHANGE 通知系统环境变量已更改
func BroadcastEnvChange() error {
	environmentW, err := syscall.UTF16PtrFromString("Environment")
	if err != nil {
		return err
	}

	ret, _, _ := procSendMessageTimeout.Call(
		HWND_BROADCAST,
		WM_SETTINGCHANGE,
		0,
		uintptr(unsafe.Pointer(environmentW)),
		SMTO_ABORTIFHUNG,
		5000,
		0,
	)

	if ret == 0 {
		return fmt.Errorf("广播 WM_SETTINGCHANGE 失败")
	}

	return nil
}

// UpdateUserPath 安全更新用户 PATH 环境变量
// 移除旧的 .jdks-link 和 java-link 绝对路径条目，添加 javaBin 到开头（如不存在）
func UpdateUserPath(javaBin string) error {
	userPath, err := GetUserEnv("PATH")
	if err != nil {
		userPath = ""
	}

	pathParts := []string{}
	if userPath != "" {
		pathParts = strings.Split(userPath, ";")
	}

	// 移除旧的绝对路径条目（.jdks-link、java-link）
	filtered := []string{}
	for _, p := range pathParts {
		pLower := strings.ToLower(p)
		if strings.Contains(pLower, ".jdks-link") || strings.Contains(pLower, "java-link") {
			continue
		}
		filtered = append(filtered, p)
	}

	// 检查 javaBin 是否已存在（支持 %JAVA_HOME%\bin 和 JAVA_HOME\bin 两种写法）
	javaBinLower := strings.ToLower(javaBin)
	found := false
	for _, p := range filtered {
		pLower := strings.ToLower(p)
		if pLower == javaBinLower || pLower == strings.ToLower(strings.ReplaceAll(javaBin, "%", "")) {
			found = true
			break
		}
	}

	var newPath string
	if !found {
		newPath = javaBin + ";" + strings.Join(filtered, ";")
	} else {
		newPath = strings.Join(filtered, ";")
	}

	return SetUserEnv("PATH", newPath)
}
