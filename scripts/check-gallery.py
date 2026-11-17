#!/usr/bin/env python3
"""Exercise the real Linux gallery on an isolated X server and accessibility bus.

Development tools: Python dbus, ffmpeg, Xvfb and xdotool. No app dependency.
"""
import argparse
import json
import os
from pathlib import Path
import subprocess
import time

import dbus

PREFIX = "org.a11y.atspi."
ROOT = "/org/a11y/atspi/accessible/root"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", required=True)
    parser.add_argument("--xvfb", default="Xvfb")
    parser.add_argument("--xdotool", default="xdotool")
    parser.add_argument("--display", help="use an existing isolated hardware display instead of starting Xvfb")
    parser.add_argument("--out", required=True)
    args = parser.parse_args()
    out = Path(args.out).resolve()
    out.mkdir(parents=True, exist_ok=True)
    processes = []
    log = (out / "gallery-native.log").open("w")

    def start(command, **kwargs):
        process = subprocess.Popen(command, stderr=log, **kwargs)
        processes.append(process)
        return process

    def wait_for(check):
        end = time.monotonic() + 60
        while True:
            result = check()
            if result:
                return result
            if time.monotonic() >= end:
                raise AssertionError("expected gallery state was not observed")
            time.sleep(.02)

    try:
        display = args.display
        if not display:
            server = start([args.xvfb, "-displayfd", "1", "-screen", "0", "1180x940x24", "-nolisten", "tcp", "-noreset"], stdout=subprocess.PIPE, text=True)
            display = ":" + server.stdout.readline().strip()
            assert display != ":", "Xvfb did not start"
        daemon = start(["dbus-daemon", "--session", "--nofork", "--print-address=1"], stdout=subprocess.PIPE, text=True)
        address = daemon.stdout.readline().strip()
        assert address, "D-Bus did not start"
        env = {**os.environ, "DISPLAY": display, "AT_SPI_BUS_ADDRESS": address, "DBUS_SESSION_BUS_ADDRESS": address}
        app = start([str(Path(args.binary).resolve())], env=env, stdout=log)
        bus = dbus.bus.BusConnection(address)

        def app_name():
            if app.poll() is not None:
                raise AssertionError("gallery exited before checks")
            for name in bus.list_names():
                if not name.startswith(":"):
                    continue
                try:
                    obj = bus.get_object(name, ROOT, introspect=False)
                    dbus.Interface(obj, PREFIX + "Accessible").GetRole(timeout=1)
                    return name
                except dbus.DBusException:
                    pass

        name = wait_for(app_name)

        def walk(path=ROOT):
            obj = bus.get_object(name, path, introspect=False)
            props = dbus.Interface(obj, "org.freedesktop.DBus.Properties")
            label = str(props.Get(PREFIX + "Accessible", "Name"))
            accessible = dbus.Interface(obj, PREFIX + "Accessible")
            yield label, obj, list(accessible.GetInterfaces())
            for _, child in accessible.GetChildren():
                yield from walk(child)

        def find(label, interface=None):
            try:
                for text, obj, interfaces in walk():
                    if text == label and (interface is None or PREFIX + interface in interfaces):
                        return obj
            except dbus.DBusException:
                return None  # Tree changed while a callback completed.

        def press(label):
            obj = wait_for(lambda: find(label, "Action"))
            assert dbus.Interface(obj, PREFIX + "Action").DoAction(dbus.Int32(0))

        def run_xdo(*command):
            return subprocess.check_output([args.xdotool, *map(str, command)], env=env, text=True).strip()

        window = run_xdo("search", "--sync", "--name", "fego").splitlines()[0]
        run_xdo("windowsize", window, 1180, 940)
        run_xdo("windowmove", window, 0, 0)
        run_xdo("windowfocus", window)
        wait_for(lambda: find("保存更改", "Action"))

        def snapshot(label):
            geometry = dict(line.split("=", 1) for line in run_xdo("getwindowgeometry", "--shell", window).splitlines())
            assert (int(geometry["WIDTH"]), int(geometry["HEIGHT"])) == (1180, 940)
            subprocess.run(["ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-f", "x11grab", "-draw_mouse", "0", "-window_id", window, "-video_size", "1180x940", "-i", display, "-frames:v", "1", "-threads", "1", str(out / (label + ".png"))], env=env, stdin=subprocess.DEVNULL, stdout=log, stderr=log, check=True, timeout=30)

        snapshot("gallery-light")
        field = wait_for(lambda: find("名称", "EditableText"))
        editable = dbus.Interface(field, PREFIX + "EditableText")
        assert editable.SetTextContents("真实窗口测试")
        wait_for(lambda: str(dbus.Interface(field, PREFIX + "Text").GetText(0, -1)) == "真实窗口测试")
        press("保存更改")
        wait_for(lambda: find("设置已保存 · 真实窗口测试"))
        run_xdo("key", "Escape")

        press("+  新建项目")
        project = wait_for(lambda: find("项目名称", "EditableText"))
        snapshot("gallery-dialog")
        assert dbus.Interface(project, PREFIX + "EditableText").SetTextContents("新项目测试")
        press("创建项目")
        wait_for(lambda: find("已创建项目：新项目测试"))
        run_xdo("key", "Escape")

        press("控件")
        wait_for(lambda: find("按钮与反馈"))
        snapshot("gallery-controls")
        search = wait_for(lambda: find("搜索你关心的内容…", "EditableText"))
        assert dbus.Interface(search, PREFIX + "Component").GrabFocus()
        snapshot("gallery-controls-focus")
        press("深色外观")
        snapshot("gallery-controls-dark")
        assert dbus.Interface(search, PREFIX + "Component").GrabFocus()
        snapshot("gallery-controls-dark-focus")
        press("深色外观")
        press("编辑器")
        wait_for(lambda: find("让想法成为作品"))
        editor = wait_for(lambda: find("", "EditableText"))
        assert dbus.Interface(editor, PREFIX + "Component").GrabFocus()
        run_xdo("key", "ctrl+a")
        run_xdo("type", "--clearmodifiers", "native keyboard input")
        wait_for(lambda: str(dbus.Interface(editor, PREFIX + "Text").GetText(0, -1)) == "native keyboard input")
        snapshot("gallery-editor")

        press("概览")
        wait_for(lambda: find("团队活跃度"))
        press("深色外观")
        snapshot("gallery-dark")
        result = {"status": "passed", "checks": ["native resize/presentation", "AT-SPI text editing", "save callback", "modal project creation", "tab retention", "native X11 keyboard editing", "light/dark rendering", "light/dark control focus"], "display": "external isolated display" if args.display else "isolated Xvfb", "renderer": {"backend": "cpu"}, "screenshots": 8}
        (out / "gallery-checks.json").write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
        print(json.dumps(result, ensure_ascii=False))
    finally:
        for process in reversed(processes):
            if process.poll() is None:
                process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
        log.close()


if __name__ == "__main__":
    main()
