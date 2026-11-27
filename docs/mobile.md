# Mobile

Use `fego.Run` from a Go main package. The operating system owns the window size, keyboard and lifecycle. Layouts receive device-pixel resize events when the window rotates or the keyboard opens. Clipboard reads deliver text asynchronously. [Example](../examples/mobile/main.go)

Android draws changed regions into reusable bitmap tiles on a native Canvas. iOS updates the affected Core Animation layers. Fego rasterizes widgets in bands and schedules frames when work arrives. Platform composition is automatic.

## Android

Install Go, a JDK with `javac`, SDK Platform 36, Android SDK Build Tools with 16 KB zip alignment support, and an Android NDK. Set `ANDROID_HOME` and `JAVA_HOME`. The helper also uses Bash, Python 3 and zip. Run it from the Go module containing the application's main package.

```sh
FEGO_APP_ID=com.example.fego scripts/build-mobile.sh android ./examples/mobile Fego.apk
adb install -r Fego.apk
```

The default APK includes arm64 and x86-64 and runs on Android 6 and later. Set `ANDROID_ARCHS=arm64` for a smaller device package. The helper signs development builds with the standard Android debug key. Set `FEGO_KEYSTORE` to use a release keystore. [Android SDK tools](https://developer.android.com/tools)

## iOS

On a Mac with Xcode, build an arm64 simulator app or an unsigned static framework for an arm64 device. The default deployment target is iOS 15.

```sh
APPLE_SDK=iphonesimulator scripts/build-mobile.sh ios ./examples/mobile Fego-simulator.ipa
unzip -qo Fego-simulator.ipa -d build/simulator
xcrun simctl install booted build/simulator/Payload/Fego.app
xcrun simctl launch booted org.fego.demo
scripts/build-mobile.sh ios-framework ./examples/mobile FegoMobile.framework
```

To embed the framework, link it into a UIKit host with UIKit, CoreText, QuartzCore, CoreGraphics, Foundation, CoreFoundation, Security and libresolv. On the main thread, attach the controller returned by `FegoMobileViewController()` to the app window.

For a signed device app, set `FEGO_APP_ID`, `IOS_PROFILE`, `IOS_IDENTITY` and `IOS_ENTITLEMENTS` to matching provisioning and signing values. Then run `scripts/build-mobile.sh ios ./examples/mobile Fego.ipa`. [Xcode](https://developer.apple.com/xcode/)
