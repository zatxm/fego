# fego

[English](README.md) | **简体中文**

轻量的纯 Go 原生桌面 UI 工具包，支持 Linux、Windows 和 macOS，无需 CGO 或 WebView。

fego 是用 Go 编写的桌面 UI 工具包，提供窗口、布局、表单、数据展示、文本编辑、浅色与深色主题。通过 Go API 创建控件并更新状态，即可构建原生桌面应用。

[示例](examples/gallery/main.go) · [性能数据](benchmarks/README.md) · [变更记录](CHANGELOG.md) · [CI](https://github.com/zatxm/fego/actions/workflows/ci.yml)

| 浅色控件 | 深色控件 |
| --- | --- |
| ![浅色控件](docs/images/controls-light.png) | ![深色控件](docs/images/controls-dark.png) |

## 平台支持

| 平台 | 窗口系统 | 架构 |
| --- | --- | --- |
| Linux | X11；Wayland 桌面通过 XWayland | amd64、arm64 |
| Windows | Win32 | amd64、arm64 |
| macOS | Cocoa | amd64、arm64 |

CI 包含 Linux amd64、Windows amd64 和 macOS arm64 的原生窗口测试，其他目标进行交叉构建检查。

Linux 运行时需要 X11 或 XWayland；本地服务器与 `libXext` 支持 MIT-SHM 时，窗口更新使用共享内存，否则回退为普通上传。中文显示需要系统中文字体，建议安装 Noto Sans CJK；Windows 和 macOS 使用系统字体。没有可用的系统拉丁字体时，会回退到内嵌 Go 字体。

## 快速开始

安装 Go，工具链要求见 [go.mod](go.mod)。在空目录中创建项目并添加依赖：

```sh
go mod init example.com/hello-fego
go get github.com/zatxm/fego
```

新建 `main.go`：

```go
package main

import (
	"log"
	"strconv"

	"github.com/zatxm/fego"
	"github.com/zatxm/fego/ui"
)

func main() {
	count := 0
	label := ui.Text("0").FontSize(40).Bold()
	button := ui.PrimaryButton("增加", func() {
		count++
		label.SetText(strconv.Itoa(count))
	})
	doc := ui.View(ui.Column(label, button).Center().Padding(24))
	if err := fego.Run(fego.WindowOptions{
		Title: "Hello fego", Width: 400, Height: 300, Content: doc,
	}); err != nil {
		log.Fatal(err)
	}
}
```

运行应用或构建可执行文件：

```sh
go run .
go build .
```

## 运行示例

克隆仓库后，可以运行包含表单、表格、弹窗、页面切换和主题切换的示例应用：

```sh
git clone https://github.com/zatxm/fego.git
cd fego
go run ./examples/gallery
```

通过参数选择深色主题或保存窗口截图：

```sh
go run ./examples/gallery -dark
go run ./examples/gallery -snapshot gallery.png
```

## 控件与功能

| 类别 | 接口 |
| --- | --- |
| 布局 | Box、Row、Column、Grid、Card、ScrollView、间距、padding、尺寸和对齐 |
| 基础控件 | Text、RichText、Button、PrimaryButton、Checkbox、Radio、Switch、Separator、Image |
| 表单 | TextInput、TextArea、密码、Field、Form、Select、ComboBox、NumberInput、Slider、Progress |
| 导航与反馈 | Tabs、Segmented、Accordion、Breadcrumbs、Menu、Dropdown、Dialog、Toast、Badge、Avatar |
| 数据 | List、Table（列排序）、Tree（展开与折叠）、LineChart、BarChart |
| 日期与文档 | Calendar、DatePicker、带行号的 CodeEditor、Markdown 常用语法 |
| 绘图 | Canvas、Painter 的矩形、圆角、圆、线、文本、图片、Theme、Bounds |
| 输入 | 鼠标、滚轮、Tab 与方向键、Unicode、选择、剪贴板、撤销、系统输入法提交文本 |
| 无障碍 | Linux AT-SPI、Windows MSAA、macOS NSAccessibility |

## 更新状态与布局

创建控件树后，使用 `SetText`、`SetValue`、`SetSelected` 等方法更新已有控件。一个控件只能放入一个父节点或窗口；程序调用 setter 不会触发用户交互回调。

事件回调在 UI 线程执行。后台 goroutine 收到数据后，通过 `window.Update(func() { ... })` 或 `app.Post` 更新界面，避免从后台直接修改控件。`fego.Run` 应从 `main` 调用；使用 `NewApp` 管理窗口时，在运行前配置 `WhenReady`，并从该回调创建窗口。

布局尺寸使用逻辑像素。`FillWidth`、`FillHeight` 分别填满对应方向，`Grow` 按父容器的主轴分配剩余空间。`ScrollView` 用于展示超出可视区域的内容，键盘聚焦时会自动滚动到目标控件。

`Tabs`、`DatePicker`、`NumberInput`、`ComboBox` 和图表等复合控件通过 `.Element` 放入布局，并用 `.Element.Width(...)` 等方法设置尺寸；保留原对象，调用其专用方法更新值。`Badge`、`Avatar` 是静态装饰，更新内容时替换节点。

列表、表格和树只绘制可见行。修改列表行内容后调用 `Refresh`，行数变化时调用 `SetCount`；修改表格数据后调用 `SetRows`。这些控件会在适用时复用滚动区域，减少重绘工作。Linux 上由 X 服务器保留窗口像素，fego 只通过有上限的临时条带绘制受损区域，不在客户端保留整窗图像。

## 文本与无障碍

中文、日文等文本依赖系统字体提供相应字形。文本编辑位置按 Unicode rune 计算，暂不支持完整的字素簇编辑、复杂文字 shaping、BiDi 或彩色 emoji。

密码字段不支持复制和剪切，无障碍接口只返回掩码。Linux 使用读屏功能时，应先开启系统无障碍服务再启动应用；应用运行后才开启读屏时，需要重启应用。

## 功能边界

- 使用 CPU 绘制；暂不支持 GPU 自定义着色器、3D、视频或原生 Wayland 后端。
- `CodeEditor` 提供文本编辑和行号，不包含 LSP 或富文本所见即所得编辑。
- Markdown 支持常用语法，尚未覆盖完整 CommonMark；HTML 按普通文本显示，链接通过回调通知应用，仅接受 http、https 和 mailto。

性能测量方法、场景和原始结果见 [性能数据](benchmarks/README.md)。
