# 性能验证

fego 使用 CPU 绘制。这里分别测量控件更新、原生窗口提交和进程资源；达到 60 FPS、减少分配，都不等于 CPU 或 RSS/PSS 必然下降。

最新的旧/新版对照见 [latest-results.json](latest-results.json)。本轮比较提交 `d62d112` 与 `602beb6`，不是发行标签之间的比较。原始样本按轮保留，CPU、RSS、PSS、峰值及帧间隔分别列出；小幅差异需结合范围判断。

## 场景与口径

| 场景 | 内容 |
| --- | --- |
| counter | 静止的数字和按钮，另测空闲区间 |
| list | 十万行虚拟列表，每帧滚动 8 px |
| grid | 600 个标签，每帧改变值和背景 |
| form | 输入、数字、勾选、滑块和进度更新 |
| image | RGBA 图片放大到窗口并重新提交 |
| richtext | 100 组 Markdown 的滚动 |

动态窗口均为 800×600；默认每次呈现后再安排下一次 60 Hz 更新，不堆积过期帧。`ready` 表示首帧完成；`start` 在预热结束后输出；`done` 在测量帧数完成后输出。原生事件适配器保留无障碍路径与保留式刷新方法。

- `appCPUUsPerFrame`：`/proc/PID/stat` 中应用所有线程的 CPU 时间，按 `CLK_TCK` 换算。
- `serverCPUUsPerFrame`：专用显示服务及其子进程在相同区间内的 CPU；`totalCPUUsPerFrame` 是两者之和。D-Bus/无障碍 registry 服务不在该进程组内。
- `rssMiB`、`pssMiB`、`peakRSSMiB`：进程驻留、共享分摊及高水位。PSS 会受到其他进程共享映射的影响。
- `goHeapResidentMiB`：内核提供 `Go: heap` 映射名称时的驻留页，不是活对象大小。
- 显示服务保留自己的缓存，其全部内存和前后变化都不能归于某一个应用。RSS 相加会重复计算共享页；PSS 与 GPU 区域不能拼成去重物理内存。
- `fps`、`frameP50Ms`、`frameP99Ms` 是工具包呈现间隔，未测物理显示完成或输入到显示器的延迟。
- 空闲 CPU 为 0 只说明低于采样分辨率。

正式对照中旧/新版使用相同夹具、帧数、构建参数和环境，轮换顺序，不强制 GC，也不设置 `GOGC` / `GOMEMLIMIT`。本次使用专用 Xvfb 软件显示服务和独立 D-Bus/真实 AT-SPI registry，无障碍查询树保持活动；与历史 Weston/XWayland 或其他主机数据不跨环境拼接。

## 构建与运行

需要 Go 1.27.3、Xvfb 和 Linux X11 运行库；编译 SDK 不需要 C 编译器。

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

本轮未重新测量 FLTK，不宣称所有指标与 FLTK 的差异在 5% 内。Windows amd64 与 macOS arm64 已通过 GitHub 托管运行器的原生窗口冒烟；其他架构运行、人工输入法/读屏及物理屏幕延迟仍需验收。历史机器原始 JSON 保留在本地并由 `.gitignore` 排除，首版仅提交最新可审阅的结果。

## 首版候选的测量结果

五轮交替，每轮60帧预热与600帧测量，共60个原生样本；同机临时性能档、CPU亲和性 `[0,1,2,3]`，专用Xvfb与真实AT-SPI registry。

| 场景 | 应用CPU旧→新 ms/帧 | 应用RSS旧→新 MiB | 应用PSS旧→新 MiB |
| --- | ---: | ---: | ---: |
| 十万行列表 | 0.817 → 0.767 | 15.926 → 15.836 | 13.399 → 13.334 |
| 表单 | 0.783 → 0.783 | 15.492 → 15.504 | 12.961 → 12.973 |
| 图片 | 1.933 → 1.883 | 13.922 → 13.949 | 11.380 → 11.409 |
| 600格网格 | 2.767 → 2.717 | 18.250 → 18.246 | 15.726 → 15.718 |
| Markdown滚动 | 0.900 → 0.900 | 16.477 → 16.469 | 13.945 → 13.960 |

列表应用CPU中位数变化 -6.1%，RSS -0.6%，PSS -0.5%。单独的内存内滚动帧基准，分配从 3320 B/op 到 1278 B/op、76 到 3 allocs/op；这些分配数据不能代替进程内存结论。

完整样本、范围与构建哈希见 [latest-results.json](latest-results.json)。其他场景按表中实际结果报告，不从定时60 FPS推断优化幅度；小差异仍可能受共享主机波动影响。

列表RSS/PSS约0.5%的中位数下降仍在样本波动范围内，尚不能视为稳定的驻留内存改善；每次滚动的累计分配下降单独报告。
