package ui

import (
	"fmt"
	"strings"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/rootxtrem3/VaultKey_Companion/internal/uistate"
)

type usbPage struct {
	root       *gtk.Box
	controller *uistate.Controller
	fields     *fieldGrid

	connect *gtk.Button
	refresh *gtk.Button
	notice  *gtk.Box
	snap    uistate.Snapshot
}

func newUSBPage(controller *uistate.Controller) *usbPage {
	page := &usbPage{controller: controller}
	root := gtk.NewBox(gtk.OrientationVertical, 0)
	root.AddCSSClass("page")
	page.root = root

	header, actions := pageHeader(
		"USB",
		"Physical detection, protocol connection, and authorization are shown separately.",
	)
	page.refresh = gtk.NewButtonFromIconName("view-refresh-symbolic")
	page.refresh.SetTooltipText("Rescan USB and refresh device status")
	page.refresh.ConnectClicked(controller.Refresh)
	actions.Append(page.refresh)

	page.connect = gtk.NewButtonWithLabel("Connect")
	page.connect.AddCSSClass("primary")
	page.connect.ConnectClicked(func() {
		if page.snap.State.DeviceOpen() {
			controller.Disconnect()
		} else {
			controller.Connect(page.snap.Port)
		}
	})
	actions.Append(page.connect)
	root.Append(header)
	root.Append(separator())

	scroller := gtk.NewScrolledWindow()
	scroller.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroller.SetVExpand(true)
	content := gtk.NewBox(gtk.OrientationVertical, 0)
	content.SetMarginStart(24)
	content.SetMarginEnd(24)
	content.SetMarginTop(8)
	content.SetMarginBottom(24)
	page.fields = &fieldGrid{values: make(map[string]*gtk.Label)}

	content.Append(sectionTitle("CONNECTION"))
	connection := newFieldGrid()
	connection.add("usb", "USB detected", "No")
	connection.add("protocol", "Protocol connection", "Disconnected")
	connection.add("authorization", "Authenticated session", "Unavailable")
	connection.add("vault", "Vault state", "Unavailable")
	page.mergeFields(connection)
	content.Append(connection.grid)

	content.Append(sectionTitle("DEVICE DESCRIPTOR"))
	descriptor := newFieldGrid()
	descriptor.add("port", "Device node", "Unavailable")
	descriptor.add("ports", "Candidate ports", "None")
	descriptor.add("vid", "Vendor ID", "Unavailable")
	descriptor.add("pid", "Product ID", "Unavailable")
	descriptor.add("serial", "Serial", "Unavailable")
	descriptor.add("manufacturer", "Manufacturer", "Unavailable")
	descriptor.add("product", "USB product", "Unavailable")
	page.mergeFields(descriptor)
	content.Append(descriptor.grid)

	content.Append(sectionTitle("INTERFACE"))
	interfaces := newFieldGrid()
	interfaces.add("interface", "Interface", "Unavailable")
	interfaces.add("class", "Class", "Unavailable")
	interfaces.add("subclass", "Subclass", "Unavailable")
	interfaces.add("interfaceProtocol", "Interface protocol", "Unavailable")
	interfaces.add("vkcp", "VKCP protocol", "Unavailable")
	page.mergeFields(interfaces)
	content.Append(interfaces.grid)

	page.notice = gtk.NewBox(gtk.OrientationHorizontal, 9)
	page.notice.AddCSSClass("notice")
	page.notice.SetMarginTop(14)
	noticeIcon := iconImage("security-high-symbolic", "changes-prevent-symbolic")
	noticeIcon.SetVAlign(gtk.AlignStart)
	noticeText := label(
		"To manage entries, unlock the vault on the device, then open USB Functions and choose “Authorize CDC 5 min”.",
		"page-subtitle",
	)
	noticeText.SetWrap(true)
	noticeText.SetHExpand(true)
	page.notice.Append(noticeIcon)
	page.notice.Append(noticeText)
	content.Append(page.notice)

	scroller.SetChild(content)
	root.Append(scroller)
	return page
}

func (p *usbPage) Widget() gtk.Widgetter { return p.root }

func (p *usbPage) mergeFields(fields *fieldGrid) {
	for key, value := range fields.values {
		p.fields.values[key] = value
	}
}

func (p *usbPage) Update(snapshot uistate.Snapshot) {
	p.snap = snapshot
	detected := snapshot.State != uistate.Disconnected || snapshot.USB.Found || len(snapshot.Ports) > 0
	p.fields.set("usb", yesNo(detected))
	if detected {
		p.fields.setClass("usb", "success")
	} else {
		p.fields.setClass("usb", "field-muted")
	}

	if snapshot.HasHello && snapshot.State.DeviceOpen() {
		p.fields.set("protocol", "Connected")
		p.fields.setClass("protocol", "success")
		p.fields.set("vkcp", fmt.Sprintf("Version %d", snapshot.Hello.Protocol))
	} else if snapshot.State == uistate.Connecting {
		p.fields.set("protocol", "Connecting")
		p.fields.setClass("protocol", "warning")
		p.fields.set("vkcp", "Negotiating")
	} else {
		p.fields.set("protocol", "Disconnected")
		p.fields.setClass("protocol", "field-muted")
		p.fields.set("vkcp", "Unavailable")
	}

	if snapshot.HasStatus {
		if snapshot.Status.Provisioning {
			p.fields.set("authorization", "Authorized")
			p.fields.setClass("authorization", "success")
		} else {
			p.fields.set("authorization", "Not authorized")
			p.fields.setClass("authorization", "warning")
		}
		if snapshot.Status.Locked {
			p.fields.set("vault", "Locked")
			p.fields.setClass("vault", "error-text")
		} else {
			p.fields.set("vault", "Unlocked")
			p.fields.setClass("vault", "success")
		}
	} else {
		p.fields.set("authorization", "Unavailable")
		p.fields.set("vault", "Unavailable")
		p.fields.setClass("authorization", "field-muted")
		p.fields.setClass("vault", "field-muted")
	}

	p.fields.set("port", dash(snapshot.Port))
	if len(snapshot.Ports) == 0 {
		p.fields.set("ports", "None")
	} else {
		p.fields.set("ports", strings.Join(snapshot.Ports, ", "))
	}
	p.fields.set("vid", dash(snapshot.USB.VID))
	p.fields.set("pid", dash(snapshot.USB.PID))
	p.fields.set("serial", dash(snapshot.USB.Serial))
	p.fields.set("manufacturer", dash(snapshot.USB.Manufacturer))
	p.fields.set("product", dash(snapshot.USB.Product))
	p.fields.set("interface", dash(snapshot.USB.Interface))
	p.fields.set("class", dash(snapshot.USB.Class))
	p.fields.set("subclass", dash(snapshot.USB.SubClass))
	p.fields.set("interfaceProtocol", dash(snapshot.USB.Protocol))

	p.notice.SetVisible(snapshot.State.DeviceOpen() && snapshot.State != uistate.Authenticated)
	p.updateAction(snapshot)
}

func (p *usbPage) updateAction(snapshot uistate.Snapshot) {
	switch snapshot.State {
	case uistate.Connecting:
		p.connect.SetLabel("Connecting…")
		p.connect.SetSensitive(false)
		p.connect.SetVisible(true)
	case uistate.Connected, uistate.Authenticated, uistate.Locked:
		p.connect.SetLabel("Disconnect")
		p.connect.SetSensitive(true)
		p.connect.SetVisible(true)
	case uistate.USBDetected:
		p.connect.SetLabel("Connect")
		p.connect.SetSensitive(true)
		p.connect.SetVisible(true)
	case uistate.Error:
		p.connect.SetLabel("Reconnect")
		p.connect.SetSensitive(snapshot.Port != "")
		p.connect.SetVisible(snapshot.Port != "")
	default:
		p.connect.SetVisible(false)
	}
}
