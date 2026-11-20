package ui

import (
	"fmt"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/rootxtrem3/VaultKey_Companion/internal/uistate"
)

type overviewPage struct {
	root       *gtk.Box
	controller *uistate.Controller

	device  *fieldGrid
	usb     *fieldGrid
	storage *fieldGrid
	session *fieldGrid

	errorBox     *gtk.Box
	errorMessage *gtk.Label
	errorHint    *gtk.Label

	connect *gtk.Button
	lock    *gtk.Button
	snap    uistate.Snapshot
}

func newOverviewPage(controller *uistate.Controller) *overviewPage {
	page := &overviewPage{controller: controller}
	root := gtk.NewBox(gtk.OrientationVertical, 0)
	root.AddCSSClass("page")
	page.root = root

	header, actions := pageHeader(
		"Overview",
		"Device identity, security state, and current USB session.",
	)
	refresh := gtk.NewButtonFromIconName("view-refresh-symbolic")
	refresh.SetTooltipText("Refresh device state (Ctrl+R)")
	refresh.ConnectClicked(controller.Refresh)
	actions.Append(refresh)

	page.lock = gtk.NewButtonWithLabel("Lock")
	page.lock.SetTooltipText("Lock the vault on the device")
	page.lock.ConnectClicked(controller.Lock)
	actions.Append(page.lock)

	page.connect = gtk.NewButtonWithLabel("Connect")
	page.connect.AddCSSClass("primary")
	page.connect.ConnectClicked(func() {
		if page.snap.State.DeviceOpen() {
			controller.Disconnect()
			return
		}
		controller.Connect(page.snap.Port)
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

	page.errorBox = gtk.NewBox(gtk.OrientationVertical, 3)
	page.errorBox.AddCSSClass("inline-error")
	page.errorBox.SetMarginTop(8)
	page.errorMessage = label("", "error-text")
	page.errorMessage.SetWrap(true)
	page.errorHint = label("", "page-subtitle")
	page.errorHint.SetWrap(true)
	page.errorBox.Append(page.errorMessage)
	page.errorBox.Append(page.errorHint)
	page.errorBox.SetVisible(false)
	content.Append(page.errorBox)

	content.Append(sectionTitle("DEVICE"))
	page.device = newFieldGrid()
	page.device.add("product", "Device", "Unavailable")
	page.device.add("firmware", "Firmware", "Unavailable")
	page.device.add("hardware", "Hardware revision", "Unavailable")
	page.device.add("protocol", "Protocol", "Unavailable")
	content.Append(page.device.grid)

	content.Append(sectionTitle("USB"))
	page.usb = newFieldGrid()
	page.usb.add("port", "Port", "Not detected")
	page.usb.add("device", "USB device", "Unavailable")
	page.usb.add("driver", "USB driver", "Unavailable")
	page.usb.add("interfaces", "Interfaces", "Unavailable")
	content.Append(page.usb.grid)

	content.Append(sectionTitle("STORAGE"))
	page.storage = newFieldGrid()
	page.storage.add("state", "Storage", "Unavailable")
	page.storage.add("credentials", "Credentials", "Unavailable")
	page.storage.add("totp", "Authenticator entries", "Unavailable")
	content.Append(page.storage.grid)

	content.Append(sectionTitle("SESSION"))
	page.session = newFieldGrid()
	page.session.add("state", "Connection", "Disconnected")
	page.session.add("vault", "Vault", "Unavailable")
	page.session.add("authorization", "CDC authorization", "Unavailable")
	page.session.add("clock", "Device clock", "Unavailable")
	content.Append(page.session.grid)

	scroller.SetChild(content)
	root.Append(scroller)
	return page
}

func (p *overviewPage) Widget() gtk.Widgetter { return p.root }

func (p *overviewPage) Update(snapshot uistate.Snapshot) {
	p.snap = snapshot

	message, hint := shortError(snapshot.Error)
	p.errorMessage.SetLabel(message)
	p.errorHint.SetLabel(hint)
	p.errorHint.SetVisible(hint != "")
	p.errorBox.SetVisible(message != "")

	if snapshot.HasHello {
		p.device.set("product", dash(snapshot.Hello.Product))
		p.device.set("firmware", dash(snapshot.Hello.Firmware))
		p.device.set("hardware", dash(snapshot.Hello.Hardware))
		p.device.set("protocol", fmt.Sprintf("VKCP %d", snapshot.Hello.Protocol))
	} else {
		p.device.set("product", "Unavailable")
		p.device.set("firmware", "Unavailable")
		p.device.set("hardware", "Unavailable")
		p.device.set("protocol", "Unavailable")
	}

	p.usb.set("port", dash(snapshot.Port))
	p.usb.set("device", describeUSB(snapshot.USB))
	if snapshot.HasStatus {
		p.usb.set("driver", enabledText(snapshot.Status.USBDriver))
		p.usb.set("interfaces", interfaceSummary(snapshot.Status.CDC, snapshot.Status.HID))
	} else {
		p.usb.set("driver", "Unavailable")
		p.usb.set("interfaces", dash(snapshot.USB.Interface))
	}

	if snapshot.HasStatus {
		p.storage.set("state", dash(snapshot.Status.Storage))
		p.storage.set("credentials", optionalCount(snapshot.Status.Credentials))
		p.storage.set("totp", optionalCount(snapshot.Status.TOTP))
		if snapshot.Status.Storage == "ready" {
			p.storage.setClass("state", "success")
		} else {
			p.storage.setClass("state", "warning")
		}
	} else {
		p.storage.set("state", "Unavailable")
		p.storage.set("credentials", "Unavailable")
		p.storage.set("totp", "Unavailable")
		p.storage.setClass("state", "field-muted")
	}

	p.session.set("state", snapshot.State.String())
	p.session.setClass("state", stateClass(snapshot.State))
	if snapshot.HasStatus {
		if snapshot.Status.Locked {
			p.session.set("vault", "Locked")
			p.session.setClass("vault", "error-text")
		} else {
			p.session.set("vault", "Unlocked")
			p.session.setClass("vault", "success")
		}
		if snapshot.Status.Provisioning {
			p.session.set("authorization", "Authorized")
			p.session.setClass("authorization", "success")
		} else {
			p.session.set("authorization", "Not authorized")
			p.session.setClass("authorization", "warning")
		}
	} else {
		p.session.set("vault", "Unavailable")
		p.session.set("authorization", "Unavailable")
		p.session.setClass("vault", "field-muted")
		p.session.setClass("authorization", "field-muted")
	}
	p.session.set("clock", clockText(snapshot))
	if snapshot.HasClock {
		p.session.setClass("clock", "")
	} else if snapshot.ClockErr != "" {
		p.session.setClass("clock", "warning")
	} else {
		p.session.setClass("clock", "field-muted")
	}

	p.updateActions(snapshot)
}

func (p *overviewPage) updateActions(snapshot uistate.Snapshot) {
	p.lock.SetVisible(snapshot.State.DeviceOpen() && snapshot.State != uistate.Locked)
	p.lock.SetSensitive(snapshot.State == uistate.Authenticated)

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

func optionalCount(value *uint64) string {
	if value == nil {
		return "Unavailable until authorized"
	}
	return fmt.Sprintf("%d", *value)
}

func enabledText(value bool) string {
	if value {
		return "Active"
	}
	return "Inactive"
}

func interfaceSummary(cdc, hid bool) string {
	result := ""
	if cdc {
		result = "CDC"
	}
	if hid {
		if result != "" {
			result += ", "
		}
		result += "HID"
	}
	if result == "" {
		return "None active"
	}
	return result
}
