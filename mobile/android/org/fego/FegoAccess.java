package org.fego;

import android.content.Context;
import android.graphics.Rect;
import android.os.Bundle;
import android.util.SparseArray;
import android.view.*;
import android.view.accessibility.*;
import java.util.HashMap;
import org.json.*;

final class FegoAccess extends AccessibilityNodeProvider
    implements AccessibilityManager.AccessibilityStateChangeListener {
  @Override
  public void onAccessibilityStateChanged(boolean enabled) {
    refresh = enabled;
    if (!enabled) {
      nodes.clear();
      ids.clear();
      focus = hover = -1;
    }
    view.postInvalidateOnAnimation();
  }

  boolean refresh;

  int frameState() {
    if (!manager.isEnabled()) return 0;
    int state = refresh ? 2 : 1;
    refresh = false;
    return state;
  }

  final FegoActivity.Surface view;
  final AccessibilityManager manager;
  final SparseArray<JSONObject> nodes = new SparseArray<>();
  HashMap<Long, Integer> ids = new HashMap<>();
  int next = 1, focus = -1, hover = -1;

  FegoAccess(FegoActivity.Surface v) {
    view = v;
    manager = (AccessibilityManager) v.getContext().getSystemService(Context.ACCESSIBILITY_SERVICE);
  }

  void update(JSONArray items) throws JSONException {
    HashMap<Long, Integer> current = new HashMap<>();
    nodes.clear();
    if (items != null)
      for (int i = 0; i < items.length(); i++) {
        JSONObject n = items.getJSONObject(i);
        long id = n.getLong("ID");
        Integer index = ids.get(id);
        if (index == null) index = next++;
        current.put(id, index);
        nodes.put(index, n);
      }
    ids = current;
    if (nodes.get(focus) == null) focus = -1;
    if (manager.isEnabled())
      view.sendAccessibilityEvent(AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED);
  }

  Rect rect(JSONObject n) {
    JSONObject b = n.optJSONObject("Bounds"),
        a = b.optJSONObject("Min"),
        z = b.optJSONObject("Max");
    return new Rect(a.optInt("X"), a.optInt("Y"), z.optInt("X"), z.optInt("Y"));
  }

  void notify(int id, int kind) {
    if (!manager.isEnabled() || view.getParent() == null) return;
    AccessibilityEvent e = AccessibilityEvent.obtain(kind);
    e.setSource(view, id);
    e.setPackageName(view.getContext().getPackageName());
    view.getParent().requestSendAccessibilityEvent(view, e);
  }

  boolean hover(MotionEvent e) {
    if (!manager.isTouchExplorationEnabled()) return false;
    int target = -1;
    if (e.getActionMasked() != MotionEvent.ACTION_HOVER_EXIT) {
      for (int i = nodes.size() - 1; i >= 0; i--)
        if (rect(nodes.valueAt(i)).contains((int) e.getX(), (int) e.getY())) {
          target = nodes.keyAt(i);
          break;
        }
    }
    if (hover != target) {
      if (target != -1) notify(target, AccessibilityEvent.TYPE_VIEW_HOVER_ENTER);
      if (hover != -1) notify(hover, AccessibilityEvent.TYPE_VIEW_HOVER_EXIT);
      hover = target;
    }
    return target != -1;
  }

  @Override
  public AccessibilityNodeInfo createAccessibilityNodeInfo(int id) {
    JSONObject n = nodes.get(id);
    if (id != View.NO_ID && n == null) return null;
    AccessibilityNodeInfo info = AccessibilityNodeInfo.obtain();
    long parent = 0;
    if (id == View.NO_ID) {
      view.onInitializeAccessibilityNodeInfo(info);
      info.setSource(view);
    } else {
      parent = n.optLong("ID");
      Integer pid = ids.get(n.optLong("Parent"));
      if (pid == null) info.setParent(view);
      else info.setParent(view, pid);
      info.setSource(view, id);
      info.setPackageName(view.getContext().getPackageName());
      String role = n.optString("Role");
      String name;
      switch (role) {
        case "button":
          name = "Button";
          break;
        case "input":
          name = "EditText";
          break;
        case "switch":
          name = "Switch";
          break;
        case "checkbox":
          name = "CheckBox";
          break;
        case "radio":
          name = "RadioButton";
          break;
        case "slider":
          name = "SeekBar";
          break;
        default:
          name = "TextView";
      }
      info.setClassName("android.widget." + name);
      info.setContentDescription(n.optString("Label"));
      info.setText(n.optString("Value"));
      info.setVisibleToUser(view.isShown());
      info.setEnabled(!n.optBoolean("Disabled"));
      info.setSelected(n.optBoolean("Selected"));
      info.setCheckable(role.equals("checkbox") || role.equals("radio") || role.equals("switch"));
      info.setChecked(n.optBoolean("Checked"));
      Rect bounds = rect(n);
      int[] pos = new int[2];
      view.getLocationOnScreen(pos);
      Rect relative = new Rect(bounds);
      if (pid != null) {
        Rect pb = rect(nodes.get(pid));
        relative.offset(-pb.left, -pb.top);
      }
      info.setBoundsInParent(relative);
      bounds.offset(pos[0], pos[1]);
      info.setBoundsInScreen(bounds);
      info.setAccessibilityFocused(id == focus);
      info.addAction(
          id == focus
              ? AccessibilityNodeInfo.ACTION_CLEAR_ACCESSIBILITY_FOCUS
              : AccessibilityNodeInfo.ACTION_ACCESSIBILITY_FOCUS);
      boolean click =
          role.equals("button") || role.equals("input") || role.equals("row") || info.isCheckable();
      if (click && info.isEnabled()) {
        info.setClickable(true);
        info.addAction(AccessibilityNodeInfo.ACTION_CLICK);
      }
    }
    for (int i = 0; i < nodes.size(); i++)
      if (nodes.valueAt(i).optLong("Parent") == parent) info.addChild(view, nodes.keyAt(i));
    return info;
  }

  @Override
  public boolean performAction(int id, int action, Bundle args) {
    if (id == View.NO_ID) return view.performAccessibilityAction(action, args);
    JSONObject n = nodes.get(id);
    if (n == null) return false;
    if (action == AccessibilityNodeInfo.ACTION_ACCESSIBILITY_FOCUS) {
      if (focus == id) return false;
      if (focus != -1) notify(focus, AccessibilityEvent.TYPE_VIEW_ACCESSIBILITY_FOCUS_CLEARED);
      focus = id;
      notify(id, AccessibilityEvent.TYPE_VIEW_ACCESSIBILITY_FOCUSED);
      return true;
    }
    if (action == AccessibilityNodeInfo.ACTION_CLEAR_ACCESSIBILITY_FOCUS && focus == id) {
      focus = -1;
      notify(id, AccessibilityEvent.TYPE_VIEW_ACCESSIBILITY_FOCUS_CLEARED);
      return true;
    }
    if (action == AccessibilityNodeInfo.ACTION_CLICK && !n.optBoolean("Disabled")) {
      FegoActivity.event(4, FegoActivity.bytes("press"), 0, n.optLong("ID"));
      notify(id, AccessibilityEvent.TYPE_VIEW_CLICKED);
      return true;
    }
    return false;
  }
}
