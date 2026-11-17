// FLTK reference for the independent fego workloads in ../fego/main.go.
#include <FL/Fl.H>
#include <FL/Fl_Box.H>
#include <FL/Fl_Button.H>
#include <FL/Fl_Double_Window.H>
#include <FL/fl_draw.H>
#include <algorithm>
#include <chrono>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <functional>
#include <string>
#include <vector>

using Clock = std::chrono::steady_clock;
static int setting(const char *name, int fallback) {
  const char *value = std::getenv(name);
  if (!value) return fallback;
  char *end;
  long n = std::strtol(value, &end, 10);
  if (*end || n < 1 || n > 1000000) {
    std::fprintf(stderr, "invalid %s\n", name);
    std::exit(2);
  }
  return int(n);
}
static double cell_value(int cell, int frame) {
  return ((cell * 7919LL + frame * 31LL) % 100000) / 100.0;
}
static std::string row_label(int row) {
  char text[100];
  std::snprintf(text, sizeof text, "Item %06d   Native UI row   %.2f", row, cell_value(row, 0));
  return text;
}

// A list has 100,000 logical rows but allocates and draws only visible rows.
class VirtualList : public Fl_Widget {
 public:
  int scroll = 0;
  VirtualList() : Fl_Widget(0, 0, 800, 600) {}
  void draw() override {
    fl_push_clip(x(), y(), w(), h());
    fl_color(FL_WHITE);
    fl_rectf(x(), y(), w(), h());
    fl_font(FL_HELVETICA, 13);
    fl_color(fl_rgb_color(24, 34, 52));
    for (int i = scroll / 36; i < 100000 && i * 36 - scroll < h(); ++i) {
      int top = y() + i * 36 - scroll;
      int baseline = top + (36 - fl_height()) / 2 + fl_height() - fl_descent();
      fl_draw(row_label(i).c_str(), x() + 12, baseline);
    }
    fl_color(fl_rgb_color(189, 197, 210));
    int thumb_y = y() + int((h() - 24) * double(scroll) / (100000 * 36 - h()));
    fl_rectf(x() + w() - 5, thumb_y, 3, 24);
    fl_pop_clip();
  }
};

class BenchWindow : public Fl_Double_Window {
 public:
  std::function<void()> presented;
  BenchWindow(int width, int height) : Fl_Double_Window(width, height, "Benchmark") {}
  void flush() override {
    Fl_Double_Window::flush();
    if (presented) presented();
  }
};

struct Benchmark {
  VirtualList *list = nullptr;
  std::vector<Fl_Box *> cells;
  int warmup = setting("BENCH_WARMUP", 120);
  int frames = setting("BENCH_FRAMES", 600);
  int frame = 0, seen = 0;
  bool ready = false, armed = false, done = false, animated = false;
  Clock::time_point last, started, next;
  std::vector<long long> intervals;

  void advance() {
    ++frame;
    armed = true;
    if (list) {
      list->scroll += 8;
      list->redraw();
    }
    for (size_t i = 0; i < cells.size(); ++i) {
      double v = cell_value(int(i), frame);
      char text[32];
      std::snprintf(text, sizeof text, "%.2f", v);
      auto *cell = cells[i];
      cell->copy_label(text);
      cell->color(v < 333 ? fl_rgb_color(246, 201, 196) :
                  v < 666 ? fl_rgb_color(244, 232, 184) : fl_rgb_color(198, 233, 200));
      cell->redraw();
    }
  }
  static void tick(void *data) { static_cast<Benchmark *>(data)->advance(); }
  void after_frame() {
    auto now = Clock::now();
    if (!ready) {
      ready = true;
      next = now;
      std::puts("ready");
      std::fflush(stdout);
    } else if (armed) {
      armed = false;
      ++seen;
      if (seen == warmup) {
        std::puts("start");
        std::fflush(stdout);
        started = now;
      } else if (seen > warmup) {
        intervals.push_back(std::chrono::duration_cast<std::chrono::microseconds>(now - last).count());
        if (int(intervals.size()) == frames) {
          std::sort(intervals.begin(), intervals.end());
          auto wall = std::chrono::duration_cast<std::chrono::microseconds>(now - started).count();
          std::printf("done {\"frames\":%d,\"wallUs\":%lld,\"p50Us\":%lld,\"p99Us\":%lld,\"maxUs\":%lld}\n",
                      frames, (long long)wall, intervals[frames / 2], intervals[frames * 99 / 100], intervals.back());
          std::fflush(stdout);
          done = true;
        }
      }
      last = now;
    } else {
      return;
    }
    if (animated && !done) {
      next += std::chrono::nanoseconds(1000000000 / 60);
      auto delay = std::chrono::duration<double>(next - Clock::now()).count();
      Fl::add_timeout(std::max(0.0, delay), tick, this);
    }
  }
};

struct Counter {
  int n = 0;
  Fl_Box *label;
  void change(int delta, bool reset = false) {
    n = reset ? 0 : n + delta;
    char text[32];
    std::snprintf(text, sizeof text, "%d", n);
    label->copy_label(text);
    label->redraw();
  }
};

int main(int argc, char **argv) {
  const char *scene = argc > 1 ? argv[1] : "counter";
  if (!std::strcmp(scene, "--version")) {
    std::printf("FLTK %d.%d.%d\n", FL_MAJOR_VERSION, FL_MINOR_VERSION, FL_PATCH_VERSION);
    return 0;
  }
  if (!std::strcmp(scene, "--self-test")) {
    if (row_label(1) != "Item 000001   Native UI row   79.19" ||
        cell_value(599, 321) != 534.32) return 1;
    std::puts("scene generators match the Go fixtures");
    return 0;
  }
  bool counter = !std::strcmp(scene, "counter");
  if (!counter && std::strcmp(scene, "grid") && std::strcmp(scene, "list")) {
    std::fprintf(stderr, "unknown scene: %s\n", scene);
    return 2;
  }
  Fl::set_font(FL_HELVETICA, "Noto Sans");
  Fl::set_font(FL_HELVETICA_BOLD, "BNoto Sans");
  BenchWindow window(counter ? 360 : 800, counter ? 260 : 600);
  Benchmark bench;
  bench.animated = !counter;
  bench.intervals.reserve(bench.frames);
  window.presented = [&bench]() { bench.after_frame(); };
  Counter state;
  window.color(FL_WHITE);
  if (counter) {
    state.label = new Fl_Box(100, 65, 160, 70, "0");
    state.label->labelsize(56);
    state.label->labelfont(FL_HELVETICA_BOLD);
    auto *minus = new Fl_Button(100, 151, 44, 32, "−");
    auto *reset = new Fl_Button(152, 151, 56, 32, "Reset");
    auto *plus = new Fl_Button(216, 151, 44, 32, "+");
    minus->callback([](Fl_Widget *, void *p) { static_cast<Counter *>(p)->change(-1); }, &state);
    reset->callback([](Fl_Widget *, void *p) { static_cast<Counter *>(p)->change(0, true); }, &state);
    plus->callback([](Fl_Widget *, void *p) { static_cast<Counter *>(p)->change(1); }, &state);
  } else if (!std::strcmp(scene, "list")) {
    bench.list = new VirtualList;
  } else {
    for (int i = 0; i < 600; ++i) {
      auto *cell = new Fl_Box((i % 20) * 40, (i / 20) * 20, 40, 20);
      cell->box(FL_FLAT_BOX);
      cell->labelsize(11);
      cell->align(FL_ALIGN_LEFT | FL_ALIGN_INSIDE | FL_ALIGN_CLIP);
      char value[32];
      double v = cell_value(i, 0);
      std::snprintf(value, sizeof value, "%.2f", v);
      cell->copy_label(value);
      cell->color(v < 333 ? fl_rgb_color(246, 201, 196) :
                  v < 666 ? fl_rgb_color(244, 232, 184) : fl_rgb_color(198, 233, 200));
      cell->labelcolor(fl_rgb_color(16, 16, 16));
      bench.cells.push_back(cell);
    }
  }
  window.end();
  window.show();
  return Fl::run();
}
