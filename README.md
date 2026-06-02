# jdks-switch - Java 版本切换工具

一个 Windows 平台的 Java 版本管理工具，实现多版本 JDK 的快速切换。

## 运行方式

支持两种运行模式：

- **命令行模式**：在终端中输入 `jdks <命令>` 直接执行，如 `jdks init`、`jdks list`
- **交互模式**：双击 `jdks.exe` 启动，进入交互式命令行，无需输入 `jdks` 前缀即可执行命令，输入 `exit` 退出

## jdks 使用说明

### 命令

| 命令 | 说明 |
|---|---|
| `jdks init` | 初始化（创建目录、设置 JAVA_HOME） |
| `jdks add <名称> <路径>` | 注册 JDK 版本 |
| `jdks list` | 查看已注册版本 |
| `jdks use <名称>` | 切换版本 |
| `jdks remove <名称>` | 移除版本 |
| `jdks -v` | 查看工具版本 |

### 示例

```bash
jdks init
jdks add jdk-8  "C:\Users\admin\.jdks\azul-1.8.0_492"
jdks add jdk-11 "C:\Users\admin\.jdks\azul-11.0.29"
jdks add jdk-17 "C:\Users\admin\.jdks\azul-17.0.19"
jdks use jdk-17
jdks list
jdks remove jdk-8
```

### 注意

- 首次 `init` 后需重启终端
- 路径用双引号包裹
- `remove` 只删链接，不删实际 JDK




## 实现方式

使用 Go 语言实现，通过 `syscall` 直接调用 Windows API，零外部依赖，编译为单文件 EXE。

### 核心原理

1. 在用户主目录下创建目录 `~/.jdks-versions/`，存储各 JDK 版本的 Junction 链接，其中 `current` 指向当前激活的版本
2. `JAVA_HOME` 永久指向 `~/.jdks-versions/current`
3. 切换版本时只需更改 `current` 的指向，无需重新配置环境变量

### Windows API 调用

| 功能 | API |
|---|---|
| Junction 创建 | `cmd /c mklink /J`（Windows 原生命令） |
| Junction 读取 | `DeviceIoControl`（`FSCTL_GET_REPARSE_POINT`） |
| Junction 删除 | `os.Remove()` |
| 环境变量持久化 | `RegOpenKeyExW` + `RegSetValueExW`（注册表 `HKCU\Environment`） |
| 环境变量读取 | `RegQueryValueExW` |
| 系统通知 | `SendMessageTimeoutW`（`WM_SETTINGCHANGE` 广播） |

## 构建与发布

需要 Go 1.21+，编译产物始终为 `jdks.exe`，通过 `-ldflags` 注入版本号。

```bash
# 开发版（版本号显示 dev）
go build -o jdks.exe .

# 发布版（版本号注入）
go build -ldflags="-s -w -X main.version=1.0.0" -o jdks.exe .
```

发布时打包为带版本号的 zip，用户解压后即可使用：

```bash
# 打包发布
compress-archive -Path jdks.exe -DestinationPath jdks-v1.0.0.zip
```

```
jdks-v1.0.0.zip
└── jdks.exe    # 用户解压后直接可用
```

安装方式：将 `jdks.exe` 所在目录添加到系统 PATH，或复制到已存在于 PATH 的目录中。

## 项目结构

```
jdks-switch/
├── main.go       # 主入口 + 5 个子命令 + 彩色输出 + 版本号
├── junction.go   # Windows Junction 操作（mklink /J + DeviceIoControl 读取）
├── env.go        # 注册表环境变量持久化（syscall）
├── go.mod
├── jdks.exe      # 编译产物
├── README.md
└── 使用说明.md
```

## 注意事项

- 仅支持 Windows 系统
- 无需管理员权限，所有操作均在当前用户权限范围内完成
- 确保添加的路径是有效的 JDK 安装目录（包含 `bin\java.exe`）
- PATH 中使用 `%JAVA_HOME%\bin` 引用，切换版本时无需重新配置 PATH
- 建议使用有意义的版本名称，如 `jdk-8`、`jdk-11`、`jdk-17`
- 在 CMD 或 PowerShell 中，路径使用双引号 + 反斜杠
- 在 Nu Shell 中，路径使用单引号 + 正斜杠

## 与 jenv 对比

| 功能 | jdks-switch | jenv / jenv-win |
|---|---|---|
| 平台支持 | 仅 Windows | Linux、macOS、Windows |
| 安装方式 | 单文件 EXE，复制即用 | 需 clone Git 仓库 + 配置 Shell Hook |
| 依赖 | 零依赖 | 依赖 bash/shell 环境 |
| 版本切换原理 | Windows Junction 链接 | Shell Hook + PATH 优先级 |
| JAVA_HOME | 写注册表，全局持久化，重启终端即生效 | 需启用 export 插件，仅当前 Shell 会话 |
| PATH 设置 | `%JAVA_HOME%\bin`，注册表持久化 | 路径前插，仅当前 Shell 会话 |
| 全局版本 | ✅ | ✅ |
| 目录级版本 | ❌ | ✅ `.java-version` 文件 |
| 多终端同时不同版本 | ❌ 全局共享 | ✅ 每个 Shell 独立 |
| 交互模式 | ✅ 双击进入交互式命令行 | ❌ |
| 插件系统 | ❌ | ✅ maven/gradle/ant 等 |
| 管理员权限 | 不需要 | 不需要 |

**适用场景**：
- **jdks-switch**：个人 Windows 开发机，只需全局切换，追求零配置开箱即用
- **jenv**：多平台开发者，需要项目级版本隔离，或有 maven/gradle 插件需求

