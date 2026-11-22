# 性能验证

fego 使用 CPU 绘制。这里分别测量控件更新、原生窗口提交和进程资源；达到 60 FPS、减少分配，都不等于 CPU 或 RSS/PSS 必然下降。

最新的旧版/新版/FLTK 对照见 [latest-results.json](latest-results.json)。本轮比较提交 `cabaf18` 与其后两轮 CPU/内存优化，不是发行标签之间的比较。原始样本按轮保留，CPU、RSS、PSS、峰值及帧间隔分别列出；小幅差异需结合范围判断。

## 场景与口径

| 场景 | 内容 |
| --- | --- |
| counter | 静止的数字和按钮，另测空闲区间 |
| list | 十万行虚拟列表，每帧滚动 8 px |
| grid | 600 个标签，每帧改变值和背景 |
| form | 输入、数字、勾选、滑块和进度更新 |
| image | RGBA 图片放大到窗口并重新提交 |
| richtext | 100 组 Markdown 的滚动 |

动态窗口均为 800×600；默认每次呈现后再安排下一次 60 Hz 更新，不堆积过期帧。`ready` 表示首帧完成；`start` 在预热结束后输出；`done` 在测量帧数完成后输出。原生事件适配器保留无障碍路径、保留式刷新、像素滚动和区域呈现方法。

- `appCPUUsPerFrame`：`/proc/PID/stat` 中应用所有线程的 CPU 时间，按 `CLK_TCK` 换算。
- `serverCPUUsPerFrame`：专用显示服务及其子进程在相同区间内的 CPU；`totalCPUUsPerFrame` 是两者之和。D-Bus/无障碍 registry 服务不在该进程组内。
- `rssMiB`、`pssMiB`、`peakRSSMiB`：进程驻留、共享分摊及高水位。PSS 会受到其他进程共享映射的影响。
- `goHeapResidentMiB`：内核提供 `Go: heap` 映射名称时的驻留页，不是活对象大小。
- 显示服务保留自己的缓存，其全部内存和前后变化都不能归于某一个应用。RSS 相加会重复计算共享页；PSS 与 GPU 区域不能拼成去重物理内存。
- `fps`、`frameP50Ms`、`frameP99Ms` 是工具包呈现间隔，未测物理显示完成或输入到显示器的延迟。
- 空闲 CPU 为 0 只说明低于采样分辨率。

正式对照中旧/新版使用相同夹具、帧数、构建参数和环境，轮换顺序，不强制 GC，也不设置 `GOGC` / `GOMEMLIMIT`。本次使用专用 Xvfb 软件显示服务和独立 D-Bus/真实 AT-SPI registry，无障碍查询树保持活动；与历史 Weston/XWayland 或其他主机数据不跨环境拼接。

## 构建与运行

需要 Go 1.27.4、Xvfb 和 Linux X11 运行库；编译 SDK 不需要 C 编译器。

```sh
bench=$(mktemp -d)
CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags '-s -w' -o "$bench/fego" ./benchmarks/fego
CGO_ENABLED=0 go build -buildvcs=false -o "$bench/run" ./benchmarks/run
Xvfb :198 -screen 0 1280x960x24 -nolisten tcp -noreset >"$bench/xvfb.log" 2>&1 &
server_pid=$!
trap 'kill "$server_pid"' EXIT
# 先确认该专用显示已启动；display 号冲突时选择另一个。
DISPLAY=:198 GDK_SCALE=1 GOMAXPROCS=4 "$bench/run" \
  -fego-bin "$bench/fego" -before-bin /absolute/path/previous-fego \
  -server-pid "$server_pid" -scenes counter,list,grid,form,image,richtext \
  -runs 5 -warmup 60 -frames 600 -idle 1s -out "$bench/results.json"
```

`previous-fego` 必须是另一个独立构建的旧版；不传 `-before-bin` 时只记录新版，不产生相对优化结论。CPU 频率和共享主机负载会影响结果；校准时旧/新版和显示服务使用相同 CPU 亲和性及电源档，结束后恢复原档。完整构建哈希、校准条件和各轮范围保存在结果中。

## 专项与诊断

```sh
CGO_ENABLED=0 go test ./internal/platform -run '^TestX11PixelCopy' -bench BenchmarkX11CopyPixels -benchmem
CGO_ENABLED=0 go test ./benchmarks/fego -run '^$' -bench BenchmarkComponents -benchmem
```

`ns/op` 是隔离操作的耗时，`B/op` 是累计分配；两者不代表进程 CPU 或 RSS/PSS。关闭字体缓存的诊断在强制 GC 后测量仍被持有的已关闭对象，单独标注，不能当作活动窗口驻留内存的收益。

诊断版本用 `go build -tags benchprofile ./benchmarks/fego` 构建；设置 `BENCH_CPU_PROFILE` 可保存 CPU profile，`BENCH_HEAP_PROFILE` 会在结束时强制 GC 并保存堆 profile。它们只用于定位问题，不用于正式性能表。

## 可选 FLTK 对照

`benchmarks/fltk/main.cc` 是独立 C++ 对照，SDK 不链接 FLTK。安装 FLTK 开发包后可使用 `c++ -O3 -DNDEBUG -std=c++17 $(fltk-config --cxxflags) benchmarks/fltk/main.cc $(fltk-config --ldflags) -o /tmp/fego-fltk` 构建，再向测量器传入 `-fltk-bin`。列表是自定义可见行控件，字体栅格化和视觉结果不保证逐像素相同。

本轮按下文条件重新测量了 FLTK；结论逐项列出，不宣称所有指标与 FLTK 的差异都在 5% 内。Windows amd64 与 macOS arm64 已通过 GitHub 托管运行器的原生窗口冒烟；其他架构运行、人工输入法/读屏及物理屏幕延迟仍需验收。历史机器原始 JSON 保留在本地并由 `.gitignore` 排除，首版仅提交最新可审阅的结果。

## 本轮优化的测量结果

五轮轮换，每轮 120 帧预热与 600 帧测量；同机 `powersave` 调速、`GOMAXPROCS=4`，专用 Xvfb（本机私有安装，支持 MIT-SHM）与桌面会话的 AT-SPI 总线；不设置 `GOGC` / `GOMEMLIMIT`。“旧”为提交 `cabaf18`，FLTK 1.4.4 按上文命令从 `benchmarks/fltk/main.cc` 构建。共享工作站上的其他进程仍在运行，表中为中位数。

| 场景 | 应用CPU µs/帧 旧→新（FLTK） | 应用PSS MiB 旧→新（FLTK） | 应用RSS MiB 旧→新（FLTK） |
| --- | ---: | ---: | ---: |
| 计数器（空闲） | 0 → 0（0） | 9.07 → 7.08（8.58） | 11.89 → 10.32（23.18） |
| 十万行列表 | 1267 → 617（850） | 13.51 → 7.16（8.50） | 16.33 → 10.10（23.02） |
| 600格网格 | 5467 → 2717（12917） | 15.75 → 11.68（8.65） | 18.59 → 15.08（23.22） |

列表应用 CPU 比 FLTK 低约 27%，应用加显示服务总 CPU 1050 对 1483 µs/帧；列表与计数器的 PSS 比 FLTK 低约 16–17%，RSS 不到 FLTK 的一半。网格应用 CPU 约为 FLTK 的 21%，但 PSS 仍高约 35%：每帧 600 个新单元格字符串会触发 Go GC，4 MiB 起步的堆目标与 GC 运行时元数据都计入 PSS。网格夹具改用 `strconv` 格式化（与 FLTK 的 `snprintf` 对应，文本不变），仍是每帧生成新字符串。共享内存段由应用与显示服务分摊，整窗重绘场景的显示服务 PSS 约增加 0.5 MiB。

其余组件场景三轮测量（旧→新，中位数）：

| 场景 | 应用CPU µs/帧 | 应用+显示服务CPU µs/帧 | 应用PSS MiB |
| --- | ---: | ---: | ---: |
| 表格 | 1367 → 750 | 1800 → 1200 | 15.65 → 10.02 |
| 树 | 1283 → 617 | 1683 → 1050 | 15.75 → 10.45 |
| 表单 | 1317 → 867 | 1550 → 1133 | 13.10 → 8.02 |
| 代码编辑器 | 2683 → 1900 | 3483 → 2367 | 13.21 → 9.48 |
| 折线图 | 9450 → 8700 | 10233 → 9167 | 11.51 → 8.31 |
| 稀疏折线图 | 3767 → 2900 | 4583 → 3383 | 11.63 → 8.17 |
| 柱状图 | 7583 → 5000 | 8417 → 5500 | 11.47 → 8.23 |
| 日历 | 933 → 500 | 983 → 567 | 11.04 → 7.20 |
| 图片 | 3350 → 2633 | 4150 → 3117 | 11.47 → 7.36 |
| 导航 | 2283 → 1183 | 3167 → 1617 | 13.75 → 8.00 |
| Markdown滚动 | 1400 → 683 | 1817 → 1133 | 14.04 → 10.16 |

主要来源：

- X11 主循环原先在锁定线程上阻塞于 `select`，每帧超过 sysmon 的系统调用阈值，运行时监控线程随之以 20 µs 间隔空转；现改为在 Go netpoller 上等待复制的连接描述符。
- X11 不保留整窗 RGBA 图像，也不在滚动时搬移客户端像素；受损区域按约 512 KiB 条带绘制上传，Expose 只重绘丢失区域。本地服务器支持 MIT-SHM 时上传走共享内存，否则回退到 XPutImage。Windows、macOS 与 `Tester` 行为不变。
- 列表滚动时库内每帧分配从约 6 KB 降到约 0.35 KB；AT-SPI 改用内置最小 D-Bus 实现，二进制从约 5.7 MB 降到 4.3 MB。
- 半透明填充与圆角矩形边缘的混合改为查表和逐行复用，像素结果不变。
- 字形混合改为与 `draw.DrawMask` 逐字节一致的专用循环（按覆盖值惰性填表，换色无开销）；文本完整落在刚绘制的不透明背景内时复制预混合字形贴图，网格应用 CPU 约减半。
- X11 绘制条带改用 mmap 内存并在小帧或空闲后释放，拉丁字体按需加载，列表与计数器 PSS 再降约 0.2–0.5 MiB。
- 渲染结果：14 个场景在四种缩放下连续 5 帧的 `Tester` 输出与旧版逐字节一致；真实图库在隔离 Xvfb 下的 8 张截图与旧版逐像素一致。

完整样本、范围与构建哈希见 [latest-results.json](latest-results.json)。其他场景按表中实际结果报告，不从定时 60 FPS 推断优化幅度；小差异仍可能受共享主机波动影响。
