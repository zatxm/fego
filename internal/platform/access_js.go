package platform

import (
	"strconv"
	"syscall/js"
)

type webAccessNode struct {
	node       js.Value
	state      AccessNode
	generation uint64
}

func (w *webWindow) SetAccessibility(nodes []AccessNode) {
	if w.accessNodes == nil {
		w.accessNodes = make(map[uint64]webAccessNode)
		w.accessRoot = js.Global().Get("document").Call("createElement", "div")
		w.accessRoot.Get("style").Set("cssText", "position:absolute;width:1px;height:1px;overflow:hidden;clip-path:inset(50%);pointer-events:none;")
		w.root.Call("appendChild", w.accessRoot)
		w.listen(w.accessRoot, "click", func(e js.Value) {
			id, err := strconv.ParseUint(e.Get("target").Call("getAttribute", "data-fego-node").String(), 10, 64)
			if err == nil && !w.accessNodes[id].state.Disabled {
				w.send(Event{Kind: AccessAction, AccessID: id, Action: "press"})
			}
		})
	}
	document := js.Global().Get("document")
	w.accessGeneration++
	var focusLabel string
	for _, n := range nodes {
		if n.Role == "input" && n.Focused {
			focusLabel = n.Label
		}
		if n.Role == "group" && n.Label == "" {
			continue
		}
		existing, found := w.accessNodes[n.ID]
		previous := existing.state
		node := existing.node
		if !found {
			tag := "div"
			switch n.Role {
			case "button":
				tag = "button"
			case "input":
				tag = "textarea"
			}
			node = document.Call("createElement", tag)
			node.Set("tabIndex", -1)
			node.Call("setAttribute", "data-fego-node", strconv.FormatUint(n.ID, 10))
			w.accessRoot.Call("appendChild", node)
		}
		if !found || previous.Role != n.Role {
			role := n.Role
			switch role {
			case "input":
				role = "textbox"
			case "image":
				role = "img"
			case "progress":
				role = "progressbar"
			case "row", "treeitem":
				role = "button"
			case "text":
				role = ""
			}
			if role != "" {
				node.Call("setAttribute", "role", role)
			}
		}
		if !found || n.Label != previous.Label {
			if n.Label != "" {
				node.Call("setAttribute", "aria-label", n.Label)
			} else if found {
				node.Call("removeAttribute", "aria-label")
			}
		}
		if n.Role == "input" {
			if !found {
				node.Set("readOnly", true)
			}
			if !found || n.Value != previous.Value {
				node.Set("value", n.Value)
			}
		} else if !found || n.Label != previous.Label {
			node.Set("textContent", n.Label)
		}
		if !found || n.Disabled != previous.Disabled {
			if n.Disabled {
				node.Call("setAttribute", "aria-disabled", "true")
			} else if found {
				node.Call("removeAttribute", "aria-disabled")
			}
		}
		switch n.Role {
		case "checkbox", "radio", "switch":
			if !found || n.Checked != previous.Checked {
				node.Call("setAttribute", "aria-checked", strconv.FormatBool(n.Checked))
			}
		case "slider", "progress":
			if !found || n.Min != previous.Min {
				node.Call("setAttribute", "aria-valuemin", strconv.FormatFloat(n.Min, 'g', -1, 64))
			}
			if !found || n.Max != previous.Max {
				node.Call("setAttribute", "aria-valuemax", strconv.FormatFloat(n.Max, 'g', -1, 64))
			}
			if !found || n.Number != previous.Number {
				node.Call("setAttribute", "aria-valuenow", strconv.FormatFloat(n.Number, 'g', -1, 64))
			}
		}
		w.accessNodes[n.ID] = webAccessNode{node, n, w.accessGeneration}
	}
	if focusLabel != "" && focusLabel != w.inputLabel {
		w.input.Call("setAttribute", "aria-label", focusLabel)
		w.inputLabel = focusLabel
	}
	for id, existing := range w.accessNodes {
		if existing.generation != w.accessGeneration {
			existing.node.Call("remove")
			delete(w.accessNodes, id)
		}
	}
}
