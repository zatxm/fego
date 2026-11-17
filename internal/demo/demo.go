// Package demo is the interactive gallery of fego's independently implemented controls.
package demo

import (
	"fmt"
	"time"

	"github.com/zatxm/fego/ui"
)

func Build() *ui.Document {
	var doc *ui.Document
	status := ui.Text("所有更改已保存").FontSize(12).TextColor(ui.RGB(113, 128, 149))
	notify := func(s string) {
		status.SetText(s)
		if doc != nil {
			doc.ShowToast(s)
		}
	}
	name := ui.TextInput("Design workspace").Placeholder("工作空间名称")
	region := ui.Select([]string{"华东 · 上海", "华北 · 北京", "亚太 · 新加坡"}, 0)
	volume := ui.Text("65%").FontSize(12).TextColor(ui.RGB(106, 120, 141)).Width(40)
	slider := ui.Slider(65, 0, 100).OnValue(func(n float64) { volume.SetText(fmt.Sprintf("%.0f%%", n)) })
	settings := ui.Card(
		ui.Text("工作空间设置").FontSize(16).Bold(),
		ui.Text("让一切按照你的习惯运行").FontSize(12).TextColor(ui.RGB(113, 128, 149)),
		ui.Separator(),
		ui.Field("名称", name, ""),
		ui.Field("服务区域", region, ""),
		ui.Switch("自动同步", true).OnToggle(func(b bool) {
			if b {
				status.SetText("自动同步已开启")
			} else {
				status.SetText("自动同步已暂停")
			}
		}),
		ui.Row(ui.Text("通知音量").Grow(1), volume), slider.FillWidth(),
		ui.Checkbox("仅接收重要通知", true),
		ui.PrimaryButton("保存更改", func() { notify("设置已保存 · " + name.Text()) }).FillWidth(),
	).Width(288).Gap(14)
	projects := [][]string{
		{"品牌设计系统", "进行中", "刚刚", "8"},
		{"移动端体验升级", "审核中", "12 分钟前", "5"},
		{"数据控制台", "进行中", "35 分钟前", "6"},
		{"产品发布页", "已完成", "1 小时前", "3"},
		{"客户支持中心", "计划中", "2 小时前", "4"},
	}
	table := ui.Table([]ui.ColumnDef{{Title: "项目"}, {Title: "状态", Width: 84}, {Title: "最近更新", Width: 110}, {Title: "成员", Width: 56}}, projects).Height(252)
	table.OnSelect(func(i int) { status.SetText("已选择：" + projects[i][0]) })
	data := []float64{24, 33, 28, 47, 42, 56, 49, 62, 59, 72, 67, 84}
	chart := ui.Canvas(520, 126, func(p *ui.Painter) {
		width := p.Bounds().W
		theme := p.Theme()
		for i := 0; i < 4; i++ {
			y := float32(i)*28 + 8
			p.Line(0, y, width, y, 1, theme.Border)
		}
		for i := 1; i < len(data); i++ {
			x1, x2 := float32(i-1)*(width-10)/11+5, float32(i)*(width-10)/11+5
			y1, y2 := float32(112-data[i-1]), float32(112-data[i])
			p.Line(x1, y1, x2, y2, 2.5, theme.Accent)
			if i == len(data)-1 {
				p.Circle(x2, y2, 4, theme.Accent)
			}
		}
	}).FillWidth()
	activity := ui.Card(ui.Row(ui.Text("团队活跃度").FontSize(16).Bold().Grow(1), ui.Text("过去 12 周").FontSize(12).TextColor(ui.RGB(113, 128, 149))), chart).Gap(8)
	projectCard := ui.Card(ui.Row(ui.Text("最近项目").FontSize(16).Bold().Grow(1), ui.Menu("更多", ui.MenuItem{Label: "刷新列表", Action: func() { table.Refresh(); notify("项目列表已更新") }}, ui.MenuItem{Label: "复制工作空间名称", Action: func() { notify("在设置中可以修改工作空间名称") }})), table).Padding(16).Gap(12)
	metric := func(title, value, detail string) *ui.Element {
		return ui.Card(ui.Text(title).FontSize(12).TextColor(ui.RGB(113, 128, 149)), ui.Row(ui.Text(value).FontSize(28).Bold().Grow(1), ui.Text("↗").FontSize(24).TextColor(ui.RGB(47, 163, 132))), ui.Text(detail).FontSize(11).TextColor(ui.RGB(47, 153, 126))).Grow(1).Gap(8).Padding(18)
	}
	overview := ui.Column(
		ui.Row(metric("进行中的项目", "24", "较上周增加 4 个"), metric("本周完成任务", "168", "完成率提升 12.8%"), metric("团队成员", "32", "今天有 28 人活跃")).Gap(16),
		ui.Row(ui.Column(activity, projectCard).Grow(1).Gap(16), settings).AlignItems(ui.Start).Gap(18),
	).Gap(18)
	checked := ui.Radio("标准方案", true)
	premium := ui.Radio("专业方案", false)
	checked.OnToggle(func(bool) { premium.SetChecked(false) })
	premium.OnToggle(func(bool) { checked.SetChecked(false) })
	progress := ui.Progress(.62).FillWidth()
	date := ui.DatePicker(time.Date(2026, 11, 10, 0, 0, 0, 0, time.Local)).Label("交付日期")
	number := ui.NumberInput(5, 1, 100, .5).Label("工作量")
	combo := ui.ComboBox([]string{"设计", "开发", "测试", "产品", "运营"}, "设计").Label("负责团队")
	controls := ui.Row(
		ui.Column(
			ui.Card(ui.Text("按钮与反馈").FontSize(17).Bold(), ui.Row(ui.PrimaryButton("主要操作", func() { notify("主要操作已执行") }), ui.Button("次要操作", func() { notify("次要操作已执行") }), ui.Button("不可用", nil).Disabled(true)), ui.Row(checked, premium), progress, ui.Button("更新进度", func() {
				n := progress.Value() + .12
				if n > 1 {
					n = 0
				}
				progress.SetValue(n)
			})),
			ui.Card(ui.Text("输入与选择").FontSize(17).Bold(), ui.TextInput("").Placeholder("搜索你关心的内容…"), ui.TextArea("你好，fego。\n这里的文本框支持选择、剪贴板与撤销。"), ui.Select([]string{"简体中文", "English", "日本語"}, 0)),
			ui.Card(ui.Text("日期与组合输入").FontSize(17).Bold(), ui.Field("交付日期", date.Element, ""), ui.Field("工作量", number.Element, ""), ui.Field("负责团队", combo.Element, "")),
		).Grow(1),
		ui.Column(ui.Card(ui.Text("可展开的树").FontSize(17).Bold(), ui.Tree(&ui.TreeNode{Label: "工作空间", Children: []*ui.TreeNode{{Label: "设计资源", Children: []*ui.TreeNode{{Label: "图标"}, {Label: "组件"}}}, {Label: "产品文档"}, {Label: "迭代计划"}}}).Height(180)), ui.Accordion(ui.Section{Title: "键盘操作", Open: true, Content: ui.Text("Tab 移动焦点，方向键调整选择。")}, ui.Section{Title: "按需绘制", Content: ui.Text("没有变化时，界面停止绘制。")})).Width(320),
	).Gap(18).AlignItems(ui.Start)
	editor := ui.CodeEditor("package main\n\nimport \"fmt\"\n\nfunc main() {\n    fmt.Println(\"你好，fego\")\n}\n").Height(340)
	editorPage := ui.Column(ui.Text("让想法成为作品").FontSize(22).Bold(), ui.Text("带行号的编辑器 · Unicode 输入 · 选择与撤销").TextColor(ui.RGB(113, 128, 149)), ui.Card(editor), ui.Markdown("### 关于这个空间\n\n**清晰的层次**，安静的色彩，以及直接的交互。\n\n- 文本、表单和数据视图\n- 保留控件状态，变化时局部更新", nil)).Gap(16)
	tabs := ui.Tabs(ui.Tab{Title: "概览", Content: ui.ScrollView(overview).Fill()}, ui.Tab{Title: "控件", Content: ui.ScrollView(controls).Fill()}, ui.Tab{Title: "编辑器", Content: ui.ScrollView(editorPage).Fill()})
	tabs.Element.Fill()
	logo := ui.Canvas(30, 30, func(p *ui.Painter) {
		p.RoundRect(ui.Rect{X: 0, Y: 0, W: 30, H: 30}, 8, ui.RGB(65, 99, 235))
		p.Rect(ui.Rect{X: 8, Y: 7, W: 14, H: 4}, ui.RGB(255, 255, 255))
		p.Rect(ui.Rect{X: 8, Y: 11, W: 4, H: 13}, ui.RGB(255, 255, 255))
		p.Rect(ui.Rect{X: 12, Y: 14, W: 8, H: 4}, ui.RGB(255, 255, 255))
	})
	nav := func(label string, index int) *ui.Element {
		return ui.Button(label, func() { tabs.SetSelected(index) }).FillWidth().Height(42)
	}
	sidebar := ui.Card(ui.Row(logo, ui.Text("fego").FontSize(25).Bold()).Gap(10), ui.Text("WORKSPACE").FontSize(10).Bold().TextColor(ui.RGB(149, 160, 179)), nav("工作概览", 0), nav("组件与交互", 1), nav("代码与文档", 2), ui.Box().Grow(1), ui.Separator(), ui.Switch("深色外观", false).OnToggle(func(dark bool) {
		if dark {
			doc.Theme(ui.Dark)
		} else {
			doc.Theme(ui.Light)
		}
	}), ui.Text("属于你的，轻量工作空间").FontSize(11).TextColor(ui.RGB(113, 128, 149))).Width(198).FillHeight().Radius(0).Padding(22, 18).Gap(18)
	newProject := func() {
		field := ui.TextInput("").Placeholder("给项目起个名字")
		doc.ShowDialog(ui.Dialog("创建新项目", ui.Field("项目名称", field, "随时可以在项目设置中修改。"), ui.Button("取消", func() { doc.CloseDialog() }), ui.PrimaryButton("创建项目", func() {
			if field.Text() == "" {
				field.Placeholder("请输入项目名称")
				return
			}
			projects = append(projects, []string{field.Text(), "计划中", "刚刚", "1"})
			table.SetRows(projects)
			doc.CloseDialog()
			notify("已创建项目：" + field.Text())
		})).Width(430))
	}
	header := ui.Row(ui.Column(ui.Text("工作空间 / 团队概览").FontSize(11).TextColor(ui.RGB(113, 128, 149)), ui.Text("把注意力，留给重要的事。").FontSize(26).Bold()).Gap(8).Grow(1), ui.PrimaryButton("+  新建项目", newProject)).Gap(18)
	body := ui.Column(header, tabs.Element, ui.Row(ui.Canvas(8, 8, func(p *ui.Painter) { p.Circle(4, 4, 3, ui.RGB(42, 173, 132)) }), status).Gap(7).Height(18)).Padding(26).Gap(22).Fill()
	doc = ui.View(ui.Row(sidebar, body).Gap(0).Fill())
	return doc
}
