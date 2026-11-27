package org.fego;

import android.app.Activity;
import android.content.*;
import android.content.ClipboardManager;
import android.graphics.*;
import android.os.*;
import android.text.*;
import android.view.*;
import android.view.accessibility.*;
import android.view.inputmethod.*;
import android.widget.*;
import java.lang.ref.WeakReference;
import java.nio.charset.StandardCharsets;
import java.util.Arrays;
import org.json.*;

public final class FegoActivity extends Activity {
  static {
    System.loadLibrary("fego");
  }

  static native void start();

  static native byte[] frame(
      Surface view, int width, int height, float scale, boolean reset, int accessible);

  static native void pointer(int kind, float x, float y, float dx, float dy, int mods);

  static native void touch(int kind, float x, float y);

  static native void event(int kind, byte[] text, int flags, long id);

  static native void edit(byte[] text, int start, int end);

  static byte[] bytes(String s) {
    return s.getBytes(StandardCharsets.UTF_8);
  }

  Surface surface;
  Editor editor;
  FrameLayout root;
  boolean syncing;

  @Override
  public void onCreate(Bundle saved) {
    super.onCreate(saved);
    if (Build.VERSION.SDK_INT >= 30) getWindow().setDecorFitsSystemWindows(false);
    getWindow().setStatusBarColor(Color.WHITE);
    getWindow().getDecorView().setSystemUiVisibility(View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR);
    getWindow().setSoftInputMode(WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE);
    root = new FrameLayout(this);
    surface = new Surface(this);
    editor = new Editor(this);
    root.addView(surface, new FrameLayout.LayoutParams(-1, -1));
    root.addView(editor, new FrameLayout.LayoutParams(1, 1));
    setContentView(root);
    // Android 15+ enforces edge-to-edge for recent target SDKs.
    root.setOnApplyWindowInsetsListener(
        (v, insets) -> {
          if (Build.VERSION.SDK_INT >= 30) {
            android.graphics.Insets bars =
                insets.getInsets(
                    WindowInsets.Type.systemBars()
                        | WindowInsets.Type.displayCutout()
                        | WindowInsets.Type.ime());
            v.setPadding(bars.left, bars.top, bars.right, bars.bottom);
          }
          return insets;
        });
    Surface.current = new WeakReference<>(surface);
    start();
    surface.requestFocus();
  }

  @Override
  public void onWindowFocusChanged(boolean on) {
    super.onWindowFocusChanged(on);
    event(3, null, on ? 1 : 0, 0);
  }

  @Override
  public void onBackPressed() {
    event(5, null, 0, 0);
    surface.postInvalidateOnAnimation();
  }

  @Override
  protected void onResume() {
    super.onResume();
    // Android can reuse the view's display list without calling onDraw
    // when returning to an Activity with unchanged dimensions.
    surface.reset = true;
    surface.invalidate();
  }

  @Override
  protected void onStop() {
    surface.releaseTiles();
    super.onStop();
  }

  @Override
  protected void onDestroy() {
    if (Surface.current.get() == surface) {
      Surface.current.clear();
      if (isFinishing()) event(6, null, 0, 0);
    }
    super.onDestroy();
  }

  final class Editor extends EditText {
    boolean active;

    Editor(Context context) {
      super(context);
      setAlpha(0);
      setPadding(0, 0, 0, 0);
      setImportantForAccessibility(View.IMPORTANT_FOR_ACCESSIBILITY_NO);
      setSingleLine(false);
      setOnEditorActionListener(
          (v, action, key) -> {
            if (key != null || action == EditorInfo.IME_NULL) return false;
            byte[] name = bytes(action == EditorInfo.IME_ACTION_NEXT ? "Tab" : "Enter");
            event(0, name, 0, 0);
            event(1, name, 0, 0);
            return true;
          });
      setTextSize(16);
      setVisibility(INVISIBLE);
      addTextChangedListener(
          new TextWatcher() {
            public void beforeTextChanged(CharSequence s, int start, int count, int after) {}

            public void onTextChanged(CharSequence s, int start, int before, int count) {}

            public void afterTextChanged(Editable t) {
              changed();
            }
          });
    }

    void changed() {
      if (syncing || !active || getSelectionStart() < 0 || getSelectionEnd() < 0) return;
      String s = getText().toString();
      edit(
          bytes(s),
          s.codePointCount(0, getSelectionStart()),
          s.codePointCount(0, getSelectionEnd()));
    }

    @Override
    protected void onSelectionChanged(int start, int end) {
      super.onSelectionChanged(start, end);
      changed();
    }

    void update(JSONObject s) throws JSONException {
      boolean show = s.getBoolean("Active");
      InputMethodManager ime = (InputMethodManager) getSystemService(INPUT_METHOD_SERVICE);
      syncing = true;
      try {
        if (!show) {
          if (active) {
            active = false;
            ime.hideSoftInputFromWindow(getWindowToken(), 0);
            setText("");
            setVisibility(INVISIBLE);
            surface.requestFocus();
          }
          return;
        }
        int type =
            s.getBoolean("Numeric")
                ? InputType.TYPE_CLASS_NUMBER
                : InputType.TYPE_CLASS_TEXT
                    | (s.getBoolean("Password")
                        ? InputType.TYPE_TEXT_VARIATION_PASSWORD
                        : InputType.TYPE_TEXT_FLAG_MULTI_LINE);
        if (getInputType() != type) {
          setInputType(type);
          if (active) ime.restartInput(this);
        }
        String text = s.getString("Text");
        if (!TextUtils.equals(getText(), text)) setText(text);
        int count = text.codePointCount(0, text.length());
        int a = text.offsetByCodePoints(0, Math.min(count, Math.max(0, s.getInt("Start"))));
        int b = text.offsetByCodePoints(0, Math.min(count, Math.max(0, s.getInt("End"))));
        if (getSelectionStart() != a || getSelectionEnd() != b) setSelection(a, b);
        JSONObject caret = s.getJSONObject("Caret").getJSONObject("Min");
        setTranslationX(caret.getInt("X"));
        setTranslationY(caret.getInt("Y"));
        if (!active) {
          active = true;
          setVisibility(VISIBLE);
          requestFocus();
          post(
              () -> {
                if (active) ime.showSoftInput(this, InputMethodManager.SHOW_IMPLICIT);
              });
        }
      } finally {
        syncing = false;
      }
    }
  }

  public static final class Surface extends View {
    static volatile WeakReference<Surface> current = new WeakReference<>(null);
    static final int TILE = 256;
    final FegoActivity app;
    final Paint paint = new Paint();
    Bitmap[] tiles = new Bitmap[0];
    int cols, visibleTiles, pointer = -1;
    boolean reset = true;
    final FegoAccess access;

    Surface(FegoActivity activity) {
      super(activity);
      app = activity;
      setFocusableInTouchMode(true);
      setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_YES);
      access = new FegoAccess(this);
    }

    static Surface currentView() {
      return current.get();
    }

    @Override
    protected void onAttachedToWindow() {
      super.onAttachedToWindow();
      access.manager.addAccessibilityStateChangeListener(access);
    }

    @Override
    protected void onDetachedFromWindow() {
      access.manager.removeAccessibilityStateChangeListener(access);
      super.onDetachedFromWindow();
    }

    static void wake() {
      Surface v = current.get();
      if (v != null) v.postInvalidateOnAnimation();
    }

    @Override
    protected void onSizeChanged(int w, int h, int ow, int oh) {
      int columns = (w + TILE - 1) / TILE;
      visibleTiles = columns * ((h + TILE - 1) / TILE);
      if (columns != cols) tiles = new Bitmap[0];
      cols = columns;
      reset = true;
    }

    void releaseTiles() {
      tiles = new Bitmap[0];
      reset = true;
    }

    Bitmap tile(int col, int row) {
      int i = row * cols + col;
      if (col < 0 || col >= cols || row < 0 || i >= visibleTiles) return null;
      // Keep tiles across keyboard and inset animations. Only a growing
      // edge or a newly visible row needs a new bitmap.
      if (tiles.length < visibleTiles) tiles = Arrays.copyOf(tiles, visibleTiles);
      Bitmap tile = tiles[i];
      int width = Math.min(TILE, getWidth() - col * TILE);
      int height = Math.min(TILE, getHeight() - row * TILE);
      if (tile == null || tile.getWidth() < width || tile.getHeight() < height) {
        tile = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888);
        tile.setDensity(Bitmap.DENSITY_NONE);
        tiles[i] = tile;
      }
      return tile;
    }

    @Override
    protected void onDraw(Canvas canvas) {
      byte[] reply =
          frame(
              this,
              getWidth(),
              getHeight(),
              getResources().getDisplayMetrics().density,
              reset,
              access.frameState());
      reset = false;
      if (reply != null) {
        try {
          apply(new JSONObject(new String(reply, StandardCharsets.UTF_8)));
        } catch (JSONException ex) {
          throw new IllegalStateException(ex);
        }
      }
      for (int i = 0; i < Math.min(visibleTiles, tiles.length); i++)
        if (tiles[i] != null)
          canvas.drawBitmap(tiles[i], (i % cols) * TILE, (i / cols) * TILE, paint);
    }

    void apply(JSONObject state) throws JSONException {
      if (state.has("Title")) app.setTitle(state.getString("Title"));
      if (state.has("Input")) app.editor.update(state.getJSONObject("Input"));
      if (state.has("Access")) access.update(state.optJSONArray("Access"));
      ClipboardManager cb = (ClipboardManager) app.getSystemService(CLIPBOARD_SERVICE);
      if (state.has("Copy")) cb.setPrimaryClip(ClipData.newPlainText("", state.getString("Copy")));
      if (state.optBoolean("Paste")) {
        ClipData clip = cb.getPrimaryClip();
        if (clip != null && clip.getItemCount() > 0) {
          CharSequence text = clip.getItemAt(0).getText();
          if (text != null) event(2, bytes(text.toString()), 0, 0);
        }
      }
      if (state.optBoolean("Finish")) app.finish();
    }

    @Override
    public AccessibilityNodeProvider getAccessibilityNodeProvider() {
      return access;
    }

    @Override
    public boolean dispatchHoverEvent(MotionEvent e) {
      return access.hover(e) || super.dispatchHoverEvent(e);
    }

    void mouse(int kind, MotionEvent e, float dx, float dy) {
      int m = e.getMetaState();
      int flags =
          ((m & KeyEvent.META_SHIFT_ON) != 0 ? 1 : 0)
              | ((m & KeyEvent.META_CTRL_ON) != 0 ? 2 : 0)
              | ((m & KeyEvent.META_ALT_ON) != 0 ? 4 : 0)
              | ((m & KeyEvent.META_META_ON) != 0 ? 8 : 0);
      pointer(kind, e.getX(), e.getY(), dx, dy, flags);
    }

    @Override
    public boolean onGenericMotionEvent(MotionEvent e) {
      if (e.getActionMasked() == MotionEvent.ACTION_SCROLL) {
        float step = 48 * getResources().getDisplayMetrics().density;
        mouse(
            4,
            e,
            -step * e.getAxisValue(MotionEvent.AXIS_HSCROLL),
            -step * e.getAxisValue(MotionEvent.AXIS_VSCROLL));
        return true;
      }
      if (e.getActionMasked() == MotionEvent.ACTION_HOVER_MOVE) {
        mouse(1, e, 0, 0);
        return true;
      }
      return super.onGenericMotionEvent(e);
    }

    @Override
    public boolean onTouchEvent(MotionEvent e) {
      if (e.isFromSource(InputDevice.SOURCE_MOUSE)) {
        switch (e.getActionMasked()) {
          case MotionEvent.ACTION_DOWN:
            mouse(0, e, 0, 0);
            return true;
          case MotionEvent.ACTION_MOVE:
            mouse(1, e, 0, 0);
            return true;
          case MotionEvent.ACTION_UP:
            mouse(2, e, 0, 0);
            return true;
          case MotionEvent.ACTION_CANCEL:
            mouse(3, e, 0, 0);
            return true;
        }
      }
      int action = e.getActionMasked(), i = e.getActionIndex();
      if (action == MotionEvent.ACTION_DOWN) {
        pointer = e.getPointerId(0);
        touch(0, e.getX(), e.getY());
        return true;
      }
      if (action == MotionEvent.ACTION_CANCEL) {
        if (pointer != -1) touch(3, 0, 0);
        pointer = -1;
        return true;
      }
      if (action == MotionEvent.ACTION_MOVE) {
        i = e.findPointerIndex(pointer);
        if (i >= 0) touch(1, e.getX(i), e.getY(i));
      }
      if ((action == MotionEvent.ACTION_UP || action == MotionEvent.ACTION_POINTER_UP)
          && pointer == e.getPointerId(i)) {
        touch(2, e.getX(i), e.getY(i));
        pointer = -1;
      }
      return true;
    }
  }

  @Override
  public boolean dispatchKeyEvent(KeyEvent e) {
    int code = e.getKeyCode();
    if (code == KeyEvent.KEYCODE_BACK
        || code == KeyEvent.KEYCODE_VOLUME_UP
        || code == KeyEvent.KEYCODE_VOLUME_DOWN) return super.dispatchKeyEvent(e);
    boolean shortcut = e.isCtrlPressed() || e.isMetaPressed();
    String key;
    switch (code) {
      case KeyEvent.KEYCODE_TAB:
        key = "Tab";
        break;
      case KeyEvent.KEYCODE_ENTER:
      case KeyEvent.KEYCODE_NUMPAD_ENTER:
        key = "Enter";
        break;
      case KeyEvent.KEYCODE_ESCAPE:
        key = "Escape";
        break;
      case KeyEvent.KEYCODE_DEL:
        key = "Backspace";
        break;
      case KeyEvent.KEYCODE_FORWARD_DEL:
        key = "Delete";
        break;
      case KeyEvent.KEYCODE_DPAD_LEFT:
        key = "ArrowLeft";
        break;
      case KeyEvent.KEYCODE_DPAD_RIGHT:
        key = "ArrowRight";
        break;
      case KeyEvent.KEYCODE_DPAD_UP:
        key = "ArrowUp";
        break;
      case KeyEvent.KEYCODE_DPAD_DOWN:
        key = "ArrowDown";
        break;
      case KeyEvent.KEYCODE_MOVE_HOME:
        key = "Home";
        break;
      case KeyEvent.KEYCODE_MOVE_END:
        key = "End";
        break;
      case KeyEvent.KEYCODE_PAGE_UP:
        key = "PageUp";
        break;
      case KeyEvent.KEYCODE_PAGE_DOWN:
        key = "PageDown";
        break;
      case KeyEvent.KEYCODE_SPACE:
        key = "Space";
        break;
      default:
        int c = e.getUnicodeChar(shortcut ? 0 : e.getMetaState());
        if (c < 32 || !Character.isValidCodePoint(c)) return super.dispatchKeyEvent(e);
        key = new String(Character.toChars(c));
    }
    if (editor.active
        && !shortcut
        && !key.equals("Tab")
        && !key.equals("Escape")
        && !key.equals("Enter")) return super.dispatchKeyEvent(e);
    if (e.getAction() != KeyEvent.ACTION_DOWN && e.getAction() != KeyEvent.ACTION_UP)
      return super.dispatchKeyEvent(e);
    int flags =
        (e.isShiftPressed() ? 1 : 0)
            | (e.isCtrlPressed() ? 2 : 0)
            | (e.isAltPressed() ? 4 : 0)
            | (e.isMetaPressed() ? 8 : 0);
    event(e.getAction() == KeyEvent.ACTION_DOWN ? 0 : 1, bytes(key), flags, 0);
    return true;
  }
}
