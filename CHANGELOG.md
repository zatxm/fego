# Changelog

## v0.1.0 — 待发布

首个公开版本；这是独立实现的纯 Go 桌面 UI，不是 mygo 的源码或 import 兼容层。

- Linux X11/XWayland、Windows Win32、macOS Cocoa 平台后端，支持 amd64/arm64 与 `CGO_ENABLED=0`。
- 保留式布局、深浅主题、表单、导航、虚拟列表/表格/树、图表、日期选择、代码编辑器和常用 Markdown。
- 原生剪贴板、键盘输入及 Linux AT-SPI / Windows MSAA / macOS NSAccessibility 适配。
- 复用滚动像素及已测量布局；限制文本和字形缓存；X11 上传缓冲限制为约 512 KiB，并在持续小范围更新后收缩。
- 使用字节反转和旋转简化 RGBA→BGRX 转换，保留大小端、非零原点、步长和透明度处理的逐像素校验。
- 关闭文本系统后清理字体和临时布局引用；关闭 X11 窗口后解除无障碍桥接对象的持有，已发布快照保持有效。
- 提供测试、跨平台构建及 Linux 原生集成测试的 GitHub CI；不自动发布或上传源码包、补丁。

### 验证范围

Linux amd64 的原生功能与性能验证见 [基准说明](benchmarks/README.md)。交叉构建不等于其他平台运行验证；人工输入法、读屏和物理屏幕延迟仍未验收。性能数据分别记录 CPU、RSS、PSS 和累计分配，不承诺所有场景都下降。
