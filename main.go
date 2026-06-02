// main.go - jdks 主入口
// Java 版本切换工具，编译为独立 EXE，无需运行时依赖

package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

// 目录常量
var JavaVersionsDir string

// 版本号，编译时通过 -ldflags 注入
var version = "dev"

// 是否支持 ANSI 颜色
var colorEnabled = true

func init() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Println("无法获取用户主目录:", err)
		os.Exit(1)
	}
	JavaVersionsDir = filepath.Join(home, ".jdks-versions")

	// 启用 Windows 虚拟终端处理，使 ANSI 转义码生效
	colorEnabled = enableVirtualTerminal()
}

// enableVirtualTerminal 启用 Windows 控制台的 ANSI 转义码支持
func enableVirtualTerminal() bool {
	var mode uint32
	// 获取标准输出句柄
	stdout := syscall.Handle(os.Stdout.Fd())
	if err := syscall.GetConsoleMode(stdout, &mode); err != nil {
		return false // 非 Console 环境（如管道重定向），不启用颜色
	}
	// ENABLE_VIRTUAL_TERMINAL_PROCESSING = 0x0004
	if mode&0x0004 != 0 {
		return true // 已启用
	}
	// syscall 未暴露 SetConsoleMode，通过 modkernel32 调用
	setConsoleMode := syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleMode")
	ret, _, _ := setConsoleMode.Call(uintptr(stdout), uintptr(mode|0x0004))
	if ret == 0 {
		return false // 启用失败，降级为无色
	}
	return true
}

// ANSI 颜色常量
const (
	ansiReset  = "\033[0m"
	ansiRed    = "\033[31m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiCyan   = "\033[36m"
	ansiGray   = "\033[90m"
)

// 根据终端能力选择颜色代码
func color(c string) string {
	if colorEnabled {
		return c
	}
	return ""
}

func reset() string {
	if colorEnabled {
		return ansiReset
	}
	return ""
}

// 彩色输出函数
func printSuccess(msg string) {
	fmt.Printf("%s%s%s\n", color(ansiGreen), msg, reset())
}

func printWarning(msg string) {
	fmt.Printf("%s%s%s\n", color(ansiYellow), msg, reset())
}

func printError(msg string) {
	fmt.Printf("%s%s%s\n", color(ansiRed), msg, reset())
}

func printCyan(msg string) {
	fmt.Printf("%s%s%s\n", color(ansiCyan), msg, reset())
}

func printGray(msg string) {
	fmt.Printf("%s%s%s\n", color(ansiGray), msg, reset())
}

// validateVersionName 校验版本名合法性
func validateVersionName(name string) error {
	if name == "" {
		return fmt.Errorf("版本名不能为空")
	}
	if name == "current" {
		return fmt.Errorf("版本名不能使用保留名 'current'")
	}
	if strings.ContainsAny(name, `\\/:*?"<>|`) {
		return fmt.Errorf("版本名包含非法字符: %s", name)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("版本名不能为 '.' 或 '..'")
	}
	return nil
}

// 验证 Java 安装路径
func validateJavaPath(javaPath string) (string, error) {
	absPath, err := filepath.Abs(javaPath)
	if err != nil {
		return "", fmt.Errorf("无法解析路径: %s", javaPath)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("路径不存在: %s", absPath)
		}
		return "", fmt.Errorf("无法访问路径: %s (%w)", absPath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("路径不是目录: %s", absPath)
	}

	javaExe := filepath.Join(absPath, "bin", "java.exe")
	if _, err := os.Stat(javaExe); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("找不到 Java 可执行文件: %s", javaExe)
		}
		return "", fmt.Errorf("无法访问 Java 可执行文件: %s (%w)", javaExe, err)
	}

	return absPath, nil
}

// addSelfToPath 将 jdks.exe 所在目录添加到用户 PATH
func addSelfToPath() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("无法获取程序路径: %w", err)
	}
	exeDir := filepath.Dir(exePath)
	absDir, err := filepath.Abs(exeDir)
	if err != nil {
		return fmt.Errorf("无法解析程序目录: %w", err)
	}

	// 读取当前用户 PATH
	userPath, err := GetUserEnv("PATH")
	if err != nil {
		userPath = ""
	}

	// 检查是否已在 PATH 中
	absDirLower := strings.ToLower(absDir)
	for _, p := range strings.Split(userPath, ";") {
		if strings.ToLower(strings.TrimSpace(p)) == absDirLower {
			return nil // 已存在
		}
	}

	// 添加到 PATH 开头
	newPath := absDir + ";" + userPath
	if err := SetUserEnv("PATH", newPath); err != nil {
		return fmt.Errorf("无法写入 PATH: %w", err)
	}

	printSuccess(fmt.Sprintf("已将 %s 添加到 PATH", absDir))
	return nil
}

// ensureInit 检查是否已初始化，未初始化则提示并返回 false
func ensureInit() bool {
	if _, err := os.Stat(JavaVersionsDir); err != nil {
		printWarning("尚未初始化，请先运行 \"jdks init\"")
		return false
	}
	return true
}

// cmdInit 初始化 jdks 环境
func cmdInit() {
	// 创建版本目录
	if err := os.MkdirAll(JavaVersionsDir, 0755); err != nil {
		printError(fmt.Sprintf("无法创建目录: %s", err))
		return
	}
	printSuccess(fmt.Sprintf("已创建目录: %s", JavaVersionsDir))

	// 创建 current junction（如果不存在）
	currentLink := filepath.Join(JavaVersionsDir, "current")
	if _, err := os.Lstat(currentLink); err != nil && os.IsNotExist(err) {
		if err := CreateJunction(currentLink, JavaVersionsDir); err != nil {
			printError(fmt.Sprintf("无法创建 junction: %s", err))
			return
		}
		printSuccess(fmt.Sprintf("已创建初始 junction: %s -> %s", currentLink, JavaVersionsDir))
	}

	// 设置 JAVA_HOME 环境变量（用户级，持久化）
	javaHome := currentLink
	if err := SetUserEnv("JAVA_HOME", javaHome); err != nil {
		printError(fmt.Sprintf("无法设置 JAVA_HOME: %s", err))
		return
	}
	os.Setenv("JAVA_HOME", javaHome)
	printSuccess(fmt.Sprintf("JAVA_HOME 已设置为: %s", javaHome))

	// 更新 PATH：使用 %JAVA_HOME%\bin 而非绝对路径
	// 这样切换版本时 PATH 自动跟随 JAVA_HOME 变化
	javaBin := `%JAVA_HOME%\bin`
	if err := UpdateUserPath(javaBin); err != nil {
		printError(fmt.Sprintf("无法更新 PATH: %s", err))
		return
	}

	// 询问用户是否将 jdks.exe 所在目录添加到 PATH
	fmt.Printf("\n是否将 jdks 添加到系统 PATH？(Y/n): ")
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))
	if input == "" || input == "y" || input == "yes" {
		if err := addSelfToPath(); err != nil {
			printWarning(fmt.Sprintf("无法将 jdks 添加到 PATH: %s", err))
		}
	} else {
		printGray("已跳过添加到 PATH，后续可手动添加或重新运行 jdks init")
	}

	printSuccess("Java 版本切换工具初始化成功！")
	printWarning("请重启终端以使环境变量更改完全生效。")
}

// cmdList 列出所有已注册的 Java 版本
func cmdList() {
	if !ensureInit() {
		return
	}

	// 读取 current junction 目标
	currentLink := filepath.Join(JavaVersionsDir, "current")
	var currentTarget string
	if _, err := os.Lstat(currentLink); err == nil {
		if target, err := ReadJunctionTarget(currentLink); err == nil {
			currentTarget = strings.TrimRight(target, `\/`)
		}
	}

	// 读取版本目录
	entries, err := os.ReadDir(JavaVersionsDir)
	if err != nil {
		printError(fmt.Sprintf("无法读取版本目录: %s", err))
		return
	}

	var versions []string
	for _, entry := range entries {
		if entry.Name() == "current" {
			continue
		}
		// 使用 Lstat 检查是否为目录或 Junction
		fullPath := filepath.Join(JavaVersionsDir, entry.Name())
		info, lerr := os.Lstat(fullPath)
		if lerr != nil {
			continue
		}
		if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			versions = append(versions, entry.Name())
		}
	}

	if len(versions) == 0 {
		printWarning("未找到 Java 版本，请使用 \"jdks add\" 添加版本。")
		return
	}

	printCyan("已注册的 Java 版本：")
	for _, ver := range versions {
		verPath := filepath.Join(JavaVersionsDir, ver)
		var verTarget string
		if target, err := ReadJunctionTarget(verPath); err == nil {
			verTarget = strings.TrimRight(target, `\/`)
		} else {
			// 非 junction，直接用路径
			absPath, _ := filepath.Abs(verPath)
			verTarget = strings.TrimRight(absPath, `\/`)
		}

		isCurrent := currentTarget != "" && verTarget != "" &&
			strings.EqualFold(currentTarget, verTarget)

		if isCurrent {
			fmt.Printf("%s  -> %s%s\n", color(ansiGreen), ver, reset())
		} else {
			fmt.Printf("     %s\n", ver)
		}
	}
}

// cmdAdd 添加新的 Java 版本
func cmdAdd(version, javaPath string) {
	if !ensureInit() {
		return
	}

	if err := validateVersionName(version); err != nil {
		printError(err.Error())
		return
	}

	targetPath := filepath.Join(JavaVersionsDir, version)

	if _, err := os.Lstat(targetPath); err == nil {
		printError(fmt.Sprintf("版本 '%s' 已存在", version))
		return
	}

	// 验证 Java 安装路径
	resolvedPath, err := validateJavaPath(javaPath)
	if err != nil {
		printError(fmt.Sprintf("无效的 Java 安装路径: %s", err))
		return
	}

	// 创建 junction 链接
	if err := CreateJunction(targetPath, resolvedPath); err != nil {
		printError(fmt.Sprintf("无法创建 junction: %s", err))
		return
	}

	printSuccess(fmt.Sprintf("已添加 Java 版本 '%s' -> %s", version, resolvedPath))
}

// cmdUse 切换到指定 Java 版本
func cmdUse(version string) {
	if !ensureInit() {
		return
	}

	targetPath := filepath.Join(JavaVersionsDir, version)

	if _, err := os.Lstat(targetPath); err != nil {
		if os.IsNotExist(err) {
			printError(fmt.Sprintf("未找到版本 '%s'", version))
		} else {
			printError(fmt.Sprintf("无法访问版本 '%s': %s", version, err))
		}
		return
	}

	currentLink := filepath.Join(JavaVersionsDir, "current")

	// 删除现有的 current junction
	if _, err := os.Lstat(currentLink); err == nil {
		if err := RemoveJunction(currentLink); err != nil {
			printError(fmt.Sprintf("无法移除旧 junction: %s", err))
			return
		}
	}

	// 创建新的 current junction
	if err := CreateJunction(currentLink, targetPath); err != nil {
		printError(fmt.Sprintf("无法创建 junction: %s", err))
		return
	}

	// 更新当前进程环境变量
	os.Setenv("JAVA_HOME", currentLink)

	printSuccess(fmt.Sprintf("已切换到 Java 版本 '%s'", version))

	// 通过执行 java -version 验证
	javaExe := filepath.Join(currentLink, "bin", "java.exe")
	if _, err := os.Stat(javaExe); err == nil {
		cmd := exec.Command(javaExe, "-version")
		output, _ := cmd.CombinedOutput()
		lines := strings.Split(string(output), "\n")
		if len(lines) > 0 && lines[0] != "" {
			printGray(fmt.Sprintf("  %s", lines[0]))
		}
	}
}

// cmdRemove 移除 Java 版本
func cmdRemove(version string) {
	if !ensureInit() {
		return
	}

	targetPath := filepath.Join(JavaVersionsDir, version)

	if _, err := os.Lstat(targetPath); err != nil {
		if os.IsNotExist(err) {
			printError(fmt.Sprintf("未找到版本 '%s'", version))
		} else {
			printError(fmt.Sprintf("无法访问版本 '%s': %s", version, err))
		}
		return
	}

	// 检查是否为当前使用的版本
	currentLink := filepath.Join(JavaVersionsDir, "current")
	if _, err := os.Lstat(currentLink); err == nil {
		currentTarget, curErr := ReadJunctionTarget(currentLink)
		if curErr == nil {
			currentTarget = strings.TrimRight(currentTarget, `\/`)

			verTarget, verErr := ReadJunctionTarget(targetPath)
			var verTargetNorm string
			if verErr == nil {
				verTargetNorm = strings.TrimRight(verTarget, `\/`)
			} else {
				absPath, _ := filepath.Abs(targetPath)
				verTargetNorm = strings.TrimRight(absPath, `\/`)
			}

			if strings.EqualFold(currentTarget, verTargetNorm) {
				RemoveJunction(currentLink)
				printWarning("当前版本已被移除，JAVA_HOME 已失效。")
			}
		}
	}

	// 安全移除版本 junction（不删除目标目录内容）
	if err := RemoveJunction(targetPath); err != nil {
		printError(fmt.Sprintf("无法移除版本: %s", err))
		return
	}

	printSuccess(fmt.Sprintf("已移除 Java 版本 '%s'", version))
}

// parseArgs 解析命令行参数，支持双引号包裹的带空格路径
// add jdk-8 "D:\Program Files\Java\jdk8" → ["add", "jdk-8", "D:\Program Files\Java\jdk8"]
func parseArgs(input string) []string {
	var parts []string
	var current strings.Builder
	inQuote := false

	for i := 0; i < len(input); i++ {
		ch := input[i]
		if ch == '"' {
			inQuote = !inQuote
			continue
		}
		if !inQuote && (ch == ' ' || ch == '\t') {
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteByte(ch)
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

// showWelcome 双击运行时显示欢迎信息
func showWelcome() {
	printCyan(fmt.Sprintf("jdks v%s - Java 版本切换工具", version))
	fmt.Println()
	fmt.Println("常用命令:")
	fmt.Println("  jdks init               初始化环境")
	fmt.Println("  jdks list               查看已注册版本")
	fmt.Println("  jdks add <名称> <路径>  注册 JDK 版本")
	fmt.Println("  jdks use <名称>         切换版本")
	fmt.Println("  jdks remove <名称>      移除版本")
	fmt.Println("  jdks -v                 查看版本号")
	fmt.Println()
	printGray("输入命令继续，输入 exit 退出")
}

// startInteractive 启动交互模式，保持窗口打开
func startInteractive() {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\njdks> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		// 解析输入的命令和参数（支持引号包裹的带空格路径）
		parts := parseArgs(input)

		// 支持 jdks 前缀："jdks init" 等同于 "init"
		if len(parts) > 0 && (parts[0] == "jdks" || parts[0] == "jdks.exe") {
			parts = parts[1:]
		}
		if len(parts) == 0 {
			continue
		}
		cmd := parts[0]

		switch cmd {
		case "exit", "quit", "q":
			fmt.Println("再见！")
			return
		case "init":
			cmdInit()
		case "list", "ls":
			cmdList()
		case "add":
			if len(parts) < 3 {
				printError("用法: add <版本名> <JDK路径>")
			} else {
				cmdAdd(parts[1], parts[2])
			}
		case "use":
			if len(parts) < 2 {
				printError("用法: use <版本名>")
			} else {
				cmdUse(parts[1])
			}
		case "remove", "rm":
			if len(parts) < 2 {
				printError("用法: remove <版本名>")
			} else {
				cmdRemove(parts[1])
			}
		case "version", "-v":
			fmt.Printf("jdks v%s\n", version)
		case "help", "-h":
			showHelp()
		case "clear", "cls":
			fmt.Print("\033[2J\033[H")
		default:
			printError(fmt.Sprintf("未知命令: %s，输入 help 查看帮助", cmd))
		}
	}
}

// showHelp 显示帮助信息
func showHelp() {
	printCyan(fmt.Sprintf("jdks - Java 版本切换工具 (v%s)", version))
	fmt.Printf("用法: jdks <命令> [参数]\n\n")
	fmt.Printf("命令:\n")
	fmt.Printf("  init              初始化 Java 版本切换工具并配置环境\n")
	fmt.Printf("  list              列出所有可用的 Java 版本\n")
	fmt.Printf("  add <版本> <路径>  添加新的 Java 版本\n")
	fmt.Printf("  use <版本>         切换到指定的 Java 版本\n")
	fmt.Printf("  remove <版本>      移除 Java 版本\n\n")
	printGray("示例:")
	fmt.Printf("  jdks init\n")
	fmt.Printf("  jdks add jdk17 \"C:\\Program Files\\Java\\jdk-17\"\n")
	fmt.Printf("  jdks use jdk17\n")
	fmt.Printf("  jdks list\n")
	fmt.Printf("  jdks remove jdk17\n")
}

// ownsConsole 检测当前程序是否拥有自己的控制台窗口
// 当从资源管理器双击、Win+R 等方式启动时，程序会创建自己的控制台
// 这种情况下程序退出后窗口会立即关闭（闪退）
func ownsConsole() bool {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getConsoleWindow := kernel32.NewProc("GetConsoleWindow")
	hwnd, _, _ := getConsoleWindow.Call()
	if hwnd == 0 {
		return false // 无控制台
	}

	// 获取控制台窗口所属进程 ID
	var consolePid uint32
	user32 := syscall.NewLazyDLL("user32.dll")
	getWindowThreadProcessId := user32.NewProc("GetWindowThreadProcessId")
	getWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&consolePid)))

	// 如果控制台窗口属于当前进程，说明是我们自己创建的控制台
	return consolePid == uint32(os.Getpid())
}

// pauseIfOwnsConsole 如果程序拥有自己的控制台窗口，等待用户按键后退出
// 防止从非终端环境启动时窗口闪退
func pauseIfOwnsConsole() {
	if ownsConsole() {
		fmt.Println("\n按回车键退出...")
		bufio.NewReader(os.Stdin).ReadString('\n')
	}
}

func main() {
	// 仅支持 Windows
	if runtime.GOOS != "windows" {
		fmt.Println("jdks 仅支持 Windows 系统")
		os.Exit(1)
	}

	// 捕获 panic，防止闪退看不到错误
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "程序异常: %v\n", r)
			pauseIfOwnsConsole()
			os.Exit(1)
		}
	}()

	if len(os.Args) < 2 {
		// 双击运行：显示欢迎信息，启动交互模式保持窗口打开
		showWelcome()
		startInteractive()
		return
	}

	command := os.Args[1]

	switch command {
	case "init":
		cmdInit()
	case "list":
		cmdList()
	case "add":
		if len(os.Args) < 4 {
			printError("用法: jdks add <版本> <路径>")
			pauseIfOwnsConsole()
			os.Exit(1)
		}
		cmdAdd(os.Args[2], os.Args[3])
	case "use":
		if len(os.Args) < 3 {
			printError("用法: jdks use <版本>")
			pauseIfOwnsConsole()
			os.Exit(1)
		}
		cmdUse(os.Args[2])
	case "remove":
		if len(os.Args) < 3 {
			printError("用法: jdks remove <版本>")
			pauseIfOwnsConsole()
			os.Exit(1)
		}
		cmdRemove(os.Args[2])
	case "-v", "--version", "version":
		fmt.Printf("jdks v%s\n", version)
	case "-h", "--help", "help":
		showHelp()
	default:
		printError(fmt.Sprintf("未知命令: %s", command))
		showHelp()
		pauseIfOwnsConsole()
		os.Exit(1)
	}

	pauseIfOwnsConsole()
}
