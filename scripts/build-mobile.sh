#!/usr/bin/env bash
set -euo pipefail
if (( $# != 3 )); then
  echo "usage: $0 android|ios|ios-framework go-main-package output" >&2
  exit 2
fi
target=$1
package=$2
out=$(python3 -c 'import os,sys;print(os.path.abspath(sys.argv[1]))' "$3")
root=$(cd "$(dirname "$0")/.." && pwd)
case "$target" in android) mobile_os=android;; ios|ios-framework) mobile_os=ios;; *) echo "unknown mobile target: $target" >&2; exit 2;; esac
pkgdir=$(GOOS="$mobile_os" GOARCH=arm64 CGO_ENABLED=1 go list -f '{{if eq .Name "main"}}{{.Dir}}{{end}}' "$package")
[[ -n "$pkgdir" ]] || { echo "expected a Go main package" >&2; exit 1; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$(dirname "$out")"
app_id=${FEGO_APP_ID:-org.fego.demo}
cat >"$tmp/start.go" <<'GO'
//go:build android || ios

package main
import "C"
import "sync/atomic"
var fegoMainRunning atomic.Bool
//export FegoStart
func FegoStart() {
 if fegoMainRunning.CompareAndSwap(false,true) {
  go func() {
   defer fegoMainRunning.Store(false)
   main()
  }()
 }
}
GO
python3 - "$pkgdir" "$tmp" <<'PY'
import json, os, sys
pkg,tmp=sys.argv[1:]
with open(tmp+'/overlay.json','w') as f:
    json.dump({'Replace':{os.path.join(pkg,'zz_fego_bootstrap.go'):tmp+'/start.go'}},f)
PY
build=(go build -overlay "$tmp/overlay.json" -trimpath -ldflags '-s -w')
case "$target" in
android)
  sdk=${ANDROID_HOME:?请设置 ANDROID_HOME}
  api=${ANDROID_API:-36}
  tools=${ANDROID_BUILD_TOOLS:-$(ls -d "$sdk"/build-tools/* | sort -V | tail -1)}
  ndk=${ANDROID_NDK_HOME:-$(ls -d "$sdk"/ndk/* | sort -V | tail -1)}
  case $(uname -s) in Darwin) host=darwin-x86_64;; Linux) host=linux-x86_64;; *) echo "use Linux or macOS to build the APK" >&2; exit 1;; esac
  if [[ -n ${JAVA_HOME:-} ]]; then export PATH="$JAVA_HOME/bin:$PATH"; fi
  android_jar="$sdk/platforms/android-$api/android.jar"
  mkdir -p "$tmp/classes" "$tmp/apk"
  for arch in ${ANDROID_ARCHS:-arm64 amd64}; do
    case "$arch" in arm64) abi=arm64-v8a; triple=aarch64;; amd64) abi=x86_64; triple=x86_64;; *) exit 2;; esac
    libdir="$tmp/apk/lib/$abi"; mkdir -p "$libdir"
    GOOS=android GOARCH="$arch" CGO_ENABLED=1 CC="$ndk/toolchains/llvm/prebuilt/$host/bin/${triple}-linux-android23-clang" \
      "${build[@]}" -buildmode=c-shared -o "$libdir/libfego.so" "$package"
    rm -f "$libdir/libfego.h"
  done
  javac --release 8 -classpath "$android_jar" -d "$tmp/classes" "$root"/mobile/android/org/fego/*.java
  jar cf "$tmp/classes.jar" -C "$tmp/classes" .
  "$tools/d8" --min-api 23 --lib "$android_jar" --output "$tmp/apk" "$tmp/classes.jar"
  "$tools/aapt2" link -I "$android_jar" --manifest "$root/mobile/android/AndroidManifest.xml" --target-sdk-version "$api" --rename-manifest-package "$app_id" -o "$tmp/base.apk"
  (cd "$tmp/apk" && zip -q -0 -r "$tmp/base.apk" classes.dex lib)
  "$tools/zipalign" -P 16 -f 4 "$tmp/base.apk" "$tmp/aligned.apk"
  key=${FEGO_KEYSTORE:-$HOME/.android/debug.keystore}
  sign=(--ks "$key")
  if [[ -z ${FEGO_KEYSTORE:-} ]]; then
    mkdir -p "$(dirname "$key")"
    if [[ ! -e "$key" ]]; then
      keytool -genkeypair -keystore "$key" -storepass android -keypass android -alias androiddebugkey -keyalg RSA -keysize 2048 -validity 10000 -dname 'CN=Android Debug,O=Android,C=US'
    fi
    sign+=(--ks-pass pass:android)
  fi
  "$tools/apksigner" sign "${sign[@]}" --out "$tmp/signed.apk" "$tmp/aligned.apk"
  "$tools/apksigner" verify "$tmp/signed.apk"
  cp "$tmp/signed.apk" "$out"
  ;;
ios|ios-framework)
  sdk=${APPLE_SDK:-iphoneos}
  deployment=${IOS_MIN:-15.0}
  case "$sdk" in iphoneos) triple=arm64-apple-ios$deployment;; iphonesimulator) triple=arm64-apple-ios$deployment-simulator;; *) exit 2;; esac
  sysroot=$(xcrun --sdk "$sdk" --show-sdk-path)
  export CGO_ENABLED=1 GOOS=ios GOARCH=arm64 CC
  CC=$(xcrun --sdk "$sdk" -f clang)
  export CGO_CFLAGS="-isysroot $sysroot -target $triple"
  export CGO_LDFLAGS="$CGO_CFLAGS"
  "${build[@]}" -buildmode=c-archive -o "$tmp/FegoMobile.a" "$package"
  xcrun --sdk "$sdk" ranlib "$tmp/FegoMobile.a"
  if [[ "$target" == ios-framework ]]; then
    framework="$tmp/FegoMobile.framework"
    mkdir -p "$framework/Headers" "$framework/Modules"
    cp "$tmp/FegoMobile.a" "$framework/FegoMobile"
    cp "$root/mobile/ios/FegoMobile.h" "$framework/Headers/"
    echo 'framework module FegoMobile { umbrella header "FegoMobile.h" export * }' >"$framework/Modules/module.modulemap"
    cp "$root/mobile/ios/Framework.plist" "$framework/Info.plist"
    mkdir -p "$out"
    cp -R "$framework/." "$out/"
  else
    app="$tmp/Payload/Fego.app"
    mkdir -p "$app"
    "$CC" -isysroot "$sysroot" -target "$triple" -fobjc-arc "$root/mobile/ios/main.m" -I"$root/mobile/ios" "$tmp/FegoMobile.a" -framework UIKit -framework CoreText -framework QuartzCore -framework CoreGraphics -framework Foundation -framework CoreFoundation -framework Security -lresolv -o "$app/Fego"
    cp "$root/mobile/ios/Info.plist" "$app/Info.plist"
    /usr/libexec/PlistBuddy -c "Set :CFBundleIdentifier $app_id" "$app/Info.plist"
    /usr/libexec/PlistBuddy -c "Set :MinimumOSVersion $deployment" "$app/Info.plist"
    if [[ "$sdk" == iphoneos ]]; then
      cp "${IOS_PROFILE:?set IOS_PROFILE to the provisioning profile}" "$app/embedded.mobileprovision"
      codesign --force --sign "${IOS_IDENTITY:?set IOS_IDENTITY}" --entitlements "${IOS_ENTITLEMENTS:?set IOS_ENTITLEMENTS}" "$app"
    else
      codesign -s - --force "$app"
    fi
    (cd "$tmp" && zip -qr app.ipa Payload)
    cp "$tmp/app.ipa" "$out"
  fi
  ;;
*) echo "unknown target $target" >&2; exit 2;;
esac
echo "$out"
