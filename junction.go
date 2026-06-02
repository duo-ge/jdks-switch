// junction.go - Windows Junction（目录联接）操作
// 使用 Windows API 直接创建、删除、读取 Junction 链接

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	modkernel32      = syscall.NewLazyDLL("kernel32.dll")
	procDeviceIoCtrl = modkernel32.NewProc("DeviceIoControl")
	procCreateFile   = modkernel32.NewProc("CreateFileW")
	procCloseHandle  = modkernel32.NewProc("CloseHandle")
)

const (
	// DeviceIoControl 常量
	FSCTL_GET_REPARSE_POINT = 0x000900A8

	// Reparse 标签
	IO_REPARSE_TAG_MOUNT_POINT = 0xA0000003

	// CreateFile 常量
	GENERIC_READ                = 0x80000000
	GENERIC_WRITE               = 0x40000000
	FILE_SHARE_READ             = 0x00000001
	FILE_SHARE_WRITE            = 0x00000002
	OPEN_EXISTING               = 3
	FILE_FLAG_BACKUP_SEMANTICS  = 0x02000000
	FILE_FLAG_OPEN_REPARSE_POINT = 0x00200000
	INVALID_HANDLE_VALUE        = ^uintptr(0)
)

// REPARSE_DATA_BUFFER 头部
type REPARSE_DATA_BUFFER_HEADER struct {
	ReparseTag           uint32
	ReparseDataLength    uint16
	Reserved             uint16
}

// MountPoint 风格的 Reparse Data Buffer
type MOUNTPOINT_REPARSE_DATA_BUFFER struct {
	Header        REPARSE_DATA_BUFFER_HEADER
	SubstituteNameOffset uint16
	SubstituteNameLength uint16
	PrintNameOffset      uint16
	PrintNameLength      uint16
	PathBuffer           [1]uint16 // 变长数据的起始
}

// CreateJunction 创建一个 Junction 链接
// 使用 cmd /c mklink /J 创建，这是 Windows 原生支持的最可靠方式
func CreateJunction(link, target string) error {
	// 确保目标路径为绝对路径
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("无法解析目标路径: %w", err)
	}

	// 确保目标目录存在
	if _, err := os.Stat(absTarget); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("目标路径不存在: %s", absTarget)
		}
		return fmt.Errorf("无法访问目标路径: %s (%w)", absTarget, err)
	}

	// 如果链接路径已存在，先删除
	if _, err := os.Lstat(link); err == nil {
		if err := os.RemoveAll(link); err != nil {
			return fmt.Errorf("无法删除已存在的路径: %w", err)
		}
	}

	// 确保链接的父目录存在
	linkDir := filepath.Dir(link)
	if err := os.MkdirAll(linkDir, 0755); err != nil {
		return fmt.Errorf("无法创建父目录: %w", err)
	}

	// 使用 mklink /J 创建 Junction
	cmd := exec.Command("cmd", "/c", "mklink", "/J", link, absTarget)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("创建 Junction 失败: %w\n%s", err, string(output))
	}

	return nil
}

// RemoveJunction 安全删除 Junction 链接（不删除目标目录内容）
func RemoveJunction(path string) error {
	return os.Remove(path)
}

// ReadJunctionTarget 读取 Junction 的目标路径
func ReadJunctionTarget(path string) (string, error) {
	// 打开目录获取句柄（读取权限即可）
	handle, err := openReparseDirForRead(path)
	if err != nil {
		return "", err
	}
	defer procCloseHandle.Call(handle)

	// 读取 REPARSE_DATA_BUFFER
	buf := make([]byte, 0x4000) // 16KB 足够
	var bytesReturned uint32

	ret, _, err := procDeviceIoCtrl.Call(
		handle,
		uintptr(FSCTL_GET_REPARSE_POINT),
		0,
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		uintptr(unsafe.Pointer(&bytesReturned)),
		0,
	)
	if ret == 0 {
		return "", fmt.Errorf("读取 Junction 目标失败: %w", err)
	}

	pBuf := (*MOUNTPOINT_REPARSE_DATA_BUFFER)(unsafe.Pointer(&buf[0]))

	// 检查是否为 Mount Point (Junction)
	if pBuf.Header.ReparseTag != IO_REPARSE_TAG_MOUNT_POINT {
		return "", fmt.Errorf("不是 Junction 链接")
	}

	// 提取 SubstituteName
	pathBufOffset := unsafe.Offsetof(MOUNTPOINT_REPARSE_DATA_BUFFER{}.PathBuffer)
	subStart := pathBufOffset + uintptr(pBuf.SubstituteNameOffset)
	subLen := int(pBuf.SubstituteNameLength) / 2

	subChars := unsafe.Slice((*uint16)(unsafe.Pointer(&buf[subStart])), subLen)
	substituteName := syscall.UTF16ToString(subChars)

	// 去掉 \??\ 前缀
	if len(substituteName) > 4 && substituteName[:4] == `\??\` {
		substituteName = substituteName[4:]
	}

	return substituteName, nil
}

// openReparseDirForRead 以读取权限打开目录并获取其句柄
func openReparseDirForRead(path string) (uintptr, error) {
	return openReparseDirWithAccess(path, GENERIC_READ)
}

// openReparseDirForWrite 以写入权限打开目录并获取其句柄（用于设置 Junction）
func openReparseDirForWrite(path string) (uintptr, error) {
	return openReparseDirWithAccess(path, GENERIC_READ|GENERIC_WRITE)
}

// openReparseDirWithAccess 以指定权限打开目录并获取其句柄
func openReparseDirWithAccess(path string, access uint32) (uintptr, error) {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}

	handle, _, err := procCreateFile.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(access),
		FILE_SHARE_READ|FILE_SHARE_WRITE,
		0,
		OPEN_EXISTING,
		FILE_FLAG_BACKUP_SEMANTICS|FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if handle == INVALID_HANDLE_VALUE {
		return 0, fmt.Errorf("无法打开目录: %w", err)
	}

	return handle, nil
}

// utf16FromString 将字符串转换为 UTF16 编码（含末尾 null）
func utf16FromString(s string) []uint16 {
	utf16, _ := syscall.UTF16FromString(s)
	return utf16
}
