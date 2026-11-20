package ui

import (
	"fmt"
	"time"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"
	"github.com/rootxtrem3/VaultKey_Companion/internal/apperr"
	"github.com/rootxtrem3/VaultKey_Companion/internal/uistate"
	"github.com/rootxtrem3/VaultKey_Companion/internal/usbdev"
	"github.com/rootxtrem3/VaultKey_Companion/pkg/vaultkey"
)

func label(text, class string) *gtk.Label {
	widget := gtk.NewLabel(text)
	widget.SetXAlign(0)
	widget.SetHAlign(gtk.AlignStart)
	if class != "" {
		widget.AddCSSClass(class)
	}
	return widget
}

func defaultDisplay() *gdk.Display { return gdk.DisplayGetDefault() }

func monoLabel(text string) *gtk.Label {
	widget := label(text, "mono")
	widget.SetSelectable(true)
	return widget
}

func ellipsize(widget *gtk.Label) *gtk.Label {
	widget.SetEllipsize(pango.EllipsizeEnd)
	widget.SetMaxWidthChars(40)
	widget.SetHExpand(true)
	return widget
}

func iconImage(name, fallback string) *gtk.Image {
	display := gdk.DisplayGetDefault()
	if display != nil {
		theme := gtk.IconThemeGetForDisplay(display)
		if theme != nil && !theme.HasIcon(name) && fallback != "" {
			name = fallback
		}
	}
	image := gtk.NewImageFromIconName(name)
	image.SetPixelSize(15)
	return image
}

func separator() *gtk.Separator {
	widget := gtk.NewSeparator(gtk.OrientationHorizontal)
	widget.AddCSSClass("separator")
	return widget
}

func pageHeader(title, subtitle string) (*gtk.Box, *gtk.Box) {
	header := gtk.NewBox(gtk.OrientationHorizontal, 16)
	header.SetMarginTop(22)
	header.SetMarginBottom(14)
	header.SetMarginStart(24)
	header.SetMarginEnd(24)

	titles := gtk.NewBox(gtk.OrientationVertical, 2)
	titles.SetHExpand(true)
	titles.Append(label(title, "page-title"))
	if subtitle != "" {
		subtitleLabel := label(subtitle, "page-subtitle")
		subtitleLabel.SetWrap(true)
		titles.Append(subtitleLabel)
	}
	header.Append(titles)

	actions := gtk.NewBox(gtk.OrientationHorizontal, 6)
	actions.SetVAlign(gtk.AlignCenter)
	header.Append(actions)
	return header, actions
}

func sectionTitle(text string) *gtk.Label {
	widget := label(text, "section-title")
	return widget
}

// fieldGrid keeps a labelled column aligned with its values.
type fieldGrid struct {
	grid   *gtk.Grid
	label  *gtk.SizeGroup
	value  *gtk.SizeGroup
	row    int
	values map[string]*gtk.Label
}

func newFieldGrid() *fieldGrid {
	grid := gtk.NewGrid()
	grid.SetColumnSpacing(24)
	grid.SetRowSpacing(8)
	grid.SetMarginTop(4)
	grid.SetMarginBottom(8)
	return &fieldGrid{
		grid:   grid,
		label:  gtk.NewSizeGroup(gtk.SizeGroupHorizontal),
		value:  gtk.NewSizeGroup(gtk.SizeGroupHorizontal),
		values: make(map[string]*gtk.Label),
	}
}

func (f *fieldGrid) add(key, caption, value string) *gtk.Label {
	labelWidget := label(caption, "field-label")
	labelWidget.SetVAlign(gtk.AlignStart)

	valueWidget := label(value, "field-value")
	valueWidget.SetSelectable(true)
	valueWidget.SetWrap(true)
	valueWidget.SetHExpand(true)

	f.label.AddWidget(labelWidget)
	f.value.AddWidget(valueWidget)
	f.grid.Attach(labelWidget, 0, f.row, 1, 1)
	f.grid.Attach(valueWidget, 1, f.row, 1, 1)
	f.values[key] = valueWidget
	f.row++
	return valueWidget
}

func (f *fieldGrid) set(key, value string) {
	if widget, ok := f.values[key]; ok {
		widget.SetLabel(value)
	}
}

func (f *fieldGrid) setClass(key, class string) {
	widget, ok := f.values[key]
	if !ok {
		return
	}
	widget.RemoveCSSClass("field-muted")
	widget.RemoveCSSClass("success")
	widget.RemoveCSSClass("warning")
	widget.RemoveCSSClass("error-text")
	widget.RemoveCSSClass("accent")
	if class != "" {
		widget.AddCSSClass(class)
	}
}

func (f *fieldGrid) widget(key string) *gtk.Label { return f.values[key] }

func dash(value string) string {
	if value == "" {
		return "—"
	}
	return value
}

func yesNo(value bool) string {
	if value {
		return "Yes"
	}
	return "No"
}

func describeUSB(info usbdev.Info) string {
	switch {
	case !info.Found:
		return "USB description unavailable"
	case info.VID != "" && info.PID != "":
		return fmt.Sprintf("%s:%s", info.VID, info.PID)
	default:
		return "USB device"
	}
}

func stateClass(state uistate.State) string {
	switch state {
	case uistate.Authenticated:
		return "success"
	case uistate.Locked, uistate.Error:
		return "error-text"
	case uistate.USBDetected, uistate.Connecting:
		return "warning"
	case uistate.Connected:
		return "accent"
	default:
		return "muted"
	}
}

func statusIconName(state uistate.State) (string, string) {
	switch state {
	case uistate.USBDetected:
		return "drive-removable-media-symbolic", "network-wired-symbolic"
	case uistate.Connected:
		return "network-wired-symbolic", "network-wired-symbolic"
	case uistate.Authenticated:
		return "security-high-symbolic", "network-wired-symbolic"
	case uistate.Locked:
		return "changes-prevent-symbolic", "security-high-symbolic"
	case uistate.Error:
		return "network-wired-symbolic", "network-wired-symbolic"
	default:
		return "network-wired-symbolic", "network-wired-symbolic"
	}
}

func iconClass(state uistate.State) string {
	switch state {
	case uistate.Authenticated, uistate.Connected:
		return "status-icon-active"
	case uistate.USBDetected, uistate.Connecting:
		return "status-icon-warning"
	case uistate.Error:
		return "status-icon-error"
	default:
		return "status-icon-disconnected"
	}
}

func clockText(snapshot uistate.Snapshot) string {
	if snapshot.HasClock {
		return time.Unix(snapshot.Clock.Unix, 0).UTC().Format("2006-01-02 15:04:05 UTC")
	}
	if snapshot.ClockErr != "" {
		return snapshot.ClockErr
	}
	return "Unavailable"
}

func shortError(info *vaultkey.ErrorInfo) (string, string) {
	if info == nil {
		return "", ""
	}
	return info.Message, info.Hint
}

func classify(err error) vaultkey.ErrorInfo { return apperr.Classify(err) }

// showError presents a compact dialog with optional expandable detail.
func showError(parent *gtk.Window, title string, err error) {
	info := classify(err)
	showErrorInfo(parent, title, info)
}

func showErrorInfo(parent *gtk.Window, title string, info vaultkey.ErrorInfo) {
	dialog := gtk.NewDialog()
	dialog.SetTitle(title)
	dialog.SetModal(true)
	dialog.SetDefaultSize(460, -1)
	if parent != nil {
		dialog.SetTransientFor(parent)
	}
	dialog.AddButton("Close", 0)
	dialog.SetDefaultResponse(0)

	body := gtk.NewBox(gtk.OrientationVertical, 8)
	body.AddCSSClass("dialog-body")
	body.Append(label(title, "dialog-title"))
	body.Append(label(info.Message, "field-value"))
	if info.Hint != "" {
		body.Append(label(info.Hint, "page-subtitle"))
	}

	detail := fmt.Sprintf("kind: %s\ncode: %s\nexit: %d", info.Kind, info.Code, info.ExitCode)
	if info.DeviceCode != "" {
		detail += "\ndevice code: " + info.DeviceCode
	}
	if info.Detail != "" {
		detail += "\n" + info.Detail
	}
	if info.Ambiguous {
		detail += "\nambiguous: the command may have been applied"
	}

	expander := gtk.NewExpander("Technical details")
	detailLabel := monoLabel(detail)
	detailLabel.SetSelectable(true)
	expander.SetChild(detailLabel)
	body.Append(expander)

	dialog.ContentArea().Append(body)
	dialog.ConnectResponse(func(int) { dialog.Destroy() })
	dialog.Present()
}

func showInfo(parent *gtk.Window, title, message string) {
	dialog := gtk.NewDialog()
	dialog.SetTitle(title)
	dialog.SetModal(true)
	dialog.SetDefaultSize(420, -1)
	if parent != nil {
		dialog.SetTransientFor(parent)
	}
	dialog.AddButton("Close", 0)
	dialog.SetDefaultResponse(0)

	body := gtk.NewBox(gtk.OrientationVertical, 8)
	body.AddCSSClass("dialog-body")
	body.Append(label(title, "dialog-title"))
	messageLabel := label(message, "field-value")
	messageLabel.SetWrap(true)
	body.Append(messageLabel)
	dialog.ContentArea().Append(body)
	dialog.ConnectResponse(func(int) { dialog.Destroy() })
	dialog.Present()
}

func confirm(parent *gtk.Window, title, message, action string, danger bool, run func()) {
	dialog := gtk.NewDialog()
	dialog.SetTitle(title)
	dialog.SetModal(true)
	dialog.SetDefaultSize(420, -1)
	if parent != nil {
		dialog.SetTransientFor(parent)
	}
	dialog.AddButton("Cancel", 0)
	accept := dialog.AddButton(action, 1)
	if button, ok := accept.(*gtk.Button); ok {
		if danger {
			button.AddCSSClass("danger")
		} else {
			button.AddCSSClass("primary")
		}
	}
	dialog.SetDefaultResponse(1)

	body := gtk.NewBox(gtk.OrientationVertical, 8)
	body.AddCSSClass("dialog-body")
	body.Append(label(title, "dialog-title"))
	messageLabel := label(message, "field-value")
	messageLabel.SetWrap(true)
	body.Append(messageLabel)
	dialog.ContentArea().Append(body)

	dialog.ConnectResponse(func(response int) {
		dialog.Destroy()
		if response == 1 && run != nil {
			run()
		}
	})
	dialog.Present()
}

// formDialog is the shared shell for add/edit dialogs.
type formDialog struct {
	dialog *gtk.Dialog
	grid   *gtk.Grid
	row    int
	parent *gtk.Window
}

func newFormDialog(parent *gtk.Window, title, acceptLabel, description string) *formDialog {
	dialog := gtk.NewDialog()
	dialog.SetTitle(title)
	dialog.SetModal(true)
	dialog.SetDefaultSize(460, -1)
	if parent != nil {
		dialog.SetTransientFor(parent)
	}
	dialog.AddButton("Cancel", 0)
	accept := dialog.AddButton(acceptLabel, 1)
	if button, ok := accept.(*gtk.Button); ok {
		button.AddCSSClass("primary")
	}
	dialog.SetDefaultResponse(1)

	body := gtk.NewBox(gtk.OrientationVertical, 10)
	body.AddCSSClass("dialog-body")
	body.Append(label(title, "dialog-title"))
	if description != "" {
		descriptionLabel := label(description, "page-subtitle")
		descriptionLabel.SetWrap(true)
		body.Append(descriptionLabel)
	}
	grid := gtk.NewGrid()
	grid.SetColumnSpacing(12)
	grid.SetRowSpacing(9)
	body.Append(grid)
	dialog.ContentArea().Append(body)
	return &formDialog{dialog: dialog, grid: grid, parent: parent}
}

func (f *formDialog) entry(caption, placeholder, initial string, secret bool) *gtk.Entry {
	field := gtk.NewEntry()
	field.SetPlaceholderText(placeholder)
	field.SetText(initial)
	field.SetHExpand(true)
	if secret {
		field.SetVisibility(false)
		field.SetInputPurpose(gtk.InputPurposePassword)
	}
	captionLabel := label(caption, "field-label")
	captionLabel.SetVAlign(gtk.AlignCenter)
	f.grid.Attach(captionLabel, 0, f.row, 1, 1)
	f.grid.Attach(field, 1, f.row, 1, 1)
	f.row++
	return field
}

func (f *formDialog) run(onAccept func() error) {
	f.dialog.ConnectResponse(func(response int) {
		if response != 1 {
			f.dialog.Destroy()
			return
		}
		if err := onAccept(); err != nil {
			showError(f.parent, "Could not complete the request", err)
			return
		}
		f.dialog.Destroy()
	})
	f.dialog.Present()
}
