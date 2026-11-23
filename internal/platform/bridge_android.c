// go:build android

#include "_cgo_export.h"
#include <android/bitmap.h>
#include <jni.h>
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
    tile = (*env)->GetMethodID(env, cls, "tile", "(II)Landroid/graphics/Bitmap;");
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
            jobject bitmap = (*env)->CallObjectMethod(env, view, tile, tx, ty);
            AndroidBitmapInfo info;
            void *dst;
            if (bitmap && AndroidBitmap_getInfo(env, bitmap, &info) == 0 && dx + w <= (int)info.width &&
                dy + h <= (int)info.height && AndroidBitmap_lockPixels(env, bitmap, &dst) == 0) {
                for (int row = 0; row < h; row++)
                    memcpy((char *)dst + (dy + row) * info.stride + dx * 4,
                           (const char *)pixels + (top - y + row) * stride + (left - x) * 4, w * 4);
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
