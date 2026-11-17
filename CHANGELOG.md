# Changelog

## v0.1.0 — 待发布

提供纯 Go 桌面 UI、原生窗口和常用控件。

- Linux X11/XWayland、Windows Win32、macOS Cocoa 平台后端，支持 amd64/arm64 与 `CGO_ENABLED=0`。
- 保留式布局、深浅主题、表单、导航、虚拟列表/表格/树、图表、日期选择、代码编辑器和常用 Markdown。
- 原生剪贴板、键盘输入及 Linux AT-SPI / Windows MSAA / macOS NSAccessibility 适配。
- 复用滚动像素及已测量布局；限制文本和字形缓存；X11 上传缓冲限制为约 512 KiB，并在持续小范围更新后收缩。
- 使用字节反转和旋转简化 RGBA→BGRX 转换，保留大小端、非零原点、步长和透明度处理的逐像素校验。
- 关闭文本系统后清理字体和临时布局引用；关闭 X11 窗口后解除无障碍桥接对象的持有，已发布快照保持有效。
- 滚动时复用有上限的列表短文本，刷新时失效；整段排除落在绘制裁切之外的文字，保留逐字形裁切和回退。
- 固定线段覆盖计算的中间舍入，避免 ARM64 融合浮点运算产生边缘像素差异。
- 提供测试、六目标跨平台构建、Linux X11/AT-SPI/race 与 Windows/macOS 原生窗口测试的 GitHub CI；不自动发布或上传源码包、补丁。

### 验证范围

Linux amd64 的原生功能与性能验证见 [基准说明](benchmarks/README.md)。Windows amd64、macOS arm64 已在 GitHub 托管运行器通过原生窗口冒烟；其他架构仅做交叉构建。人工输入法、读屏和物理屏幕延迟仍未验收。性能数据分别记录 CPU、RSS、PSS 和累计分配，不承诺所有场景都下降。
