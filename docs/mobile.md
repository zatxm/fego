# Mobile

The same Go layouts and widgets run in one native Android or iOS window. On mobile, fego uses the Gio platform backend for the window, touch input, keyboard, clipboard, and presentation. The fego controls and CPU painter still draw the UI. Dirty tiles are uploaded to the mobile surface.

Use `fego.Run` from `main`, as in the [mobile example](../examples/mobile/main.go). The system manages the window size and app lifecycle. Use flexible layouts that can fit a phone, resize when the keyboard opens, and scroll as needed. Android and iOS builds require CGO and their native toolchains. On mobile, clipboard paste is delivered asynchronously to the active text field.

## Android

Install Go 1.27.4 or later, a JDK, the Android SDK build tools, and an Android NDK. Set `ANDROID_HOME` to the SDK directory and `JAVA_HOME` to the JDK. Install the platform matching `-targetsdk`.

```sh
go run gioui.org/cmd/gogio@v0.10.3 -target android -arch arm64,amd64 -minsdk 23 -targetsdk 36 -appid com.example.fego -o Mobile.apk ./examples/mobile
adb install -r Mobile.apk
```

## iOS

On macOS with Xcode and the iOS SDK installed, use the same example. A device build needs a provisioning profile for the app identifier and its associated signing identity.

```sh
go run gioui.org/cmd/gogio@v0.10.3 -target ios -arch arm64 -minsdk 15 -appid com.example.fego -signkey App.mobileprovision -o Mobile.ipa ./examples/mobile
```

To compile the arm64 iOS code without signing, build a framework:

```sh
go run gioui.org/cmd/gogio@v0.10.3 -target ios -arch arm64 -minsdk 15 -buildmode archive -o Mobile.framework ./examples/mobile
```

[Android toolchain](https://gioui.org/doc/install/android) · [iOS toolchain](https://gioui.org/doc/install/ios)
