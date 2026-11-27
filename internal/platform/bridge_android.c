// go:build android

#include "_cgo_export.h"
#include <android/bitmap.h>
#include <jni.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

// The Activity class owns only a weak reference to the currently attached view.
static JavaVM *vm;
static jclass host;
static jmethodID wake, tile, current;
static _Thread_local JNIEnv *frame_env;
static _Thread_local jobject frame_view;
extern void FegoStart(void);

JNIEXPORT jint JNICALL JNI_OnLoad(JavaVM *jvm, void *unused) {
    JNIEnv *env = NULL;
    if ((*jvm)->GetEnv(jvm, (void **)&env, JNI_VERSION_1_6) != JNI_OK)
        return JNI_ERR;
    jclass cls = (*env)->FindClass(env, "org/fego/FegoActivity$Surface");
    if (!cls)
        return JNI_ERR;
    host = (*env)->NewGlobalRef(env, cls);
    wake = (*env)->GetStaticMethodID(env, cls, "wake", "()V");
    tile = (*env)->GetMethodID(env, cls, "tile", "(IIIIIII)Landroid/graphics/Bitmap;");
    current = (*env)->GetStaticMethodID(env, cls, "currentView", "()Lorg/fego/FegoActivity$Surface;");
    if (!host || !wake || !tile || !current)
        return JNI_ERR;
    vm = jvm;
    return JNI_VERSION_1_6;
}

void fe_wake(void) {
    if (!vm)
        return;
    JNIEnv *env = NULL;
    int attach = (*vm)->GetEnv(vm, (void **)&env, JNI_VERSION_1_6) == JNI_EDETACHED;
    if (attach && (*vm)->AttachCurrentThread(vm, &env, NULL) != JNI_OK)
        return;
    (*env)->CallStaticVoidMethod(env, host, wake);
    if (attach)
        (*vm)->DetachCurrentThread(vm);
}

#define TILE 256
// A uniform opaque region needs no Java bitmap storage.
static uint32_t solid_color(const uint8_t *pixels, int w, int h, int stride) {
    if (pixels[3] != 255)
        return 0;
    uint32_t color;
    memcpy(&color, pixels, sizeof(color));
    for (int row = 0; row < h; row++) {
        const uint8_t *line = pixels + row * stride;
        for (int col = 0; col < w; col++) {
            uint32_t pixel;
            memcpy(&pixel, line + col * 4, sizeof(pixel));
            if (pixel != color)
                return 0;
        }
    }
    return 0xff000000u | ((uint32_t)pixels[0] << 16) |
           ((uint32_t)pixels[1] << 8) | pixels[2];
}

void fe_upload(int x, int y, int width, int height, int stride, const void *pixels) {
    JNIEnv *env = frame_env;
    jobject view = frame_view;
    if (!env) {
        // A custom view can present directly from an input callback.
        if (!vm || (*vm)->GetEnv(vm, (void **)&env, JNI_VERSION_1_6) != JNI_OK)
            return;
        view = (*env)->CallStaticObjectMethod(env, host, current);
        if (!view)
            return;
    }
    for (int top = y; top < y + height;) {
        int ty = top / TILE, dy = top % TILE, h = TILE - dy;
        if (h > y + height - top)
            h = y + height - top;
        for (int left = x; left < x + width;) {
            int tx = left / TILE, dx = left % TILE, w = TILE - dx;
            if (w > x + width - left)
                w = x + width - left;
            const uint8_t *src = (const uint8_t *)pixels + (top - y) * stride + (left - x) * 4;
            uint32_t color = solid_color(src, w, h, stride);
            jobject bitmap = (*env)->CallObjectMethod(env, view, tile, tx, ty, dx, dy, w, h, (jint)color);
            AndroidBitmapInfo info;
            void *dst;
            if (bitmap && AndroidBitmap_getInfo(env, bitmap, &info) == 0 && dx + w <= (int)info.width &&
                dy + h <= (int)info.height && AndroidBitmap_lockPixels(env, bitmap, &dst) == 0) {
                for (int row = 0; row < h; row++)
                    memcpy((char *)dst + (dy + row) * info.stride + dx * 4,
                           src + row * stride, w * 4);
                AndroidBitmap_unlockPixels(env, bitmap);
            }
            if (bitmap)
                (*env)->DeleteLocalRef(env, bitmap);
            left += w;
        }
        top += h;
    }
    if (!frame_view) {
        (*env)->DeleteLocalRef(env, view);
        fe_wake();
    }
}

JNIEXPORT void JNICALL Java_org_fego_FegoActivity_start(JNIEnv *env, jclass cls) { FegoStart(); }

JNIEXPORT jbyteArray JNICALL Java_org_fego_FegoActivity_frame(JNIEnv *env, jclass cls, jobject view, jint w,
                                                              jint h, jfloat scale, jboolean reset,
                                                              jint accessible) {
    frame_env = env;
    frame_view = view;
    char *state = fe_frame(w, h, scale, reset, accessible);
    frame_view = NULL;
    frame_env = NULL;
    if (!state)
        return NULL;
    int n = (int)strlen(state);
    jbyteArray data = (*env)->NewByteArray(env, n);
    if (data)
        (*env)->SetByteArrayRegion(env, data, 0, n, (jbyte *)state);
    free(state);
    return data;
}
JNIEXPORT void JNICALL Java_org_fego_FegoActivity_pointer(JNIEnv *env, jclass cls, jint kind, jfloat x,
                                                          jfloat y, jfloat dx, jfloat dy, jint flags) {
    fe_pointer(kind, x, y, dx, dy, flags);
}

JNIEXPORT void JNICALL Java_org_fego_FegoActivity_touch(JNIEnv *env, jclass cls, jint kind, jfloat x,
                                                        jfloat y) {
    fe_touch(kind, x, y);
}
JNIEXPORT void JNICALL Java_org_fego_FegoActivity_edit(JNIEnv *env, jclass cls, jbyteArray data, jint start,
                                                       jint end) {
    int n = (*env)->GetArrayLength(env, data);
    if (n > 4 * 1024 * 1024)
        return;
    jbyte *p = (*env)->GetByteArrayElements(env, data, NULL);
    if (p) {
        fe_edit((char *)p, n, start, end);
        (*env)->ReleaseByteArrayElements(env, data, p, JNI_ABORT);
    }
}
JNIEXPORT void JNICALL Java_org_fego_FegoActivity_event(JNIEnv *env, jclass cls, jint kind, jbyteArray data,
                                                        jint flags, jlong id) {
    int n = data ? (*env)->GetArrayLength(env, data) : 0;
    if (n > 4 * 1024 * 1024)
        return;
    jbyte *p = n ? (*env)->GetByteArrayElements(env, data, NULL) : NULL;
    fe_event(kind, (char *)p, n, flags, id);
    if (p)
        (*env)->ReleaseByteArrayElements(env, data, p, JNI_ABORT);
}
